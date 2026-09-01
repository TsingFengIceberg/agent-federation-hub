package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

// StepDefinition is the only workflow input that is specific to a domain.
// The Hub stores the definition and remote Task IDs, but never inspects the
// provider's prompts, tools, checkpoints, or internal graph.
type StepDefinition struct {
	ID               string `json:"id"`
	AgentID          string `json:"agentId,omitempty"`
	Skill            string `json:"skill,omitempty"`
	Text             string `json:"text"`
	Required         bool   `json:"required"`
	CompensationText string `json:"compensationText,omitempty"`
}

type WorkflowDefinition struct {
	ID    string           `json:"id,omitempty"`
	Name  string           `json:"name"`
	Steps []StepDefinition `json:"steps"`
}

type WorkflowResult struct {
	Workflow core.Workflow `json:"workflow"`
	Errors   []string      `json:"errors,omitempty"`
}

// StartWorkflow creates a durable aggregate before contacting any Provider.
// Each branch is then submitted independently; a failed branch is recorded in
// the aggregate while successful branches continue and remain recoverable.
func (c *Coordinator) StartWorkflow(ctx context.Context, tenantID string, definition WorkflowDefinition) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	if tenantID == "" || len(definition.Steps) == 0 {
		return WorkflowResult{}, errors.New("workflow tenant and at least one step are required")
	}
	seen := make(map[string]struct{}, len(definition.Steps))
	steps := make([]core.WorkflowStep, 0, len(definition.Steps))
	for index, definitionStep := range definition.Steps {
		stepID := definitionStep.ID
		if stepID == "" {
			stepID = fmt.Sprintf("step-%d", index+1)
		}
		if _, exists := seen[stepID]; exists {
			return WorkflowResult{}, fmt.Errorf("duplicate workflow step %q", stepID)
		}
		seen[stepID] = struct{}{}
		if definitionStep.AgentID == "" && definitionStep.Skill == "" {
			return WorkflowResult{}, fmt.Errorf("workflow step %q requires agentId or skill", stepID)
		}
		if definitionStep.Text == "" {
			return WorkflowResult{}, fmt.Errorf("workflow step %q text is required", stepID)
		}
		steps = append(steps, core.WorkflowStep{
			ID: stepID, AgentID: definitionStep.AgentID, Skill: definitionStep.Skill,
			State: core.TaskStateUnknown, Required: definitionStep.Required,
			CompensationText: definitionStep.CompensationText,
		})
	}
	workflowID := definition.ID
	if workflowID == "" {
		workflowID = core.NewID()
	}
	now := time.Now().UTC()
	workflow, err := store.CreateWorkflow(ctx, core.Workflow{
		ID: workflowID, TenantID: tenantID, Name: definition.Name,
		State: core.WorkflowStatePending, Steps: steps, CreatedAt: now, UpdatedAt: now,
	}, core.WorkflowEvent{Type: "workflow.created", Source: "hub", State: core.WorkflowStatePending, CreatedAt: now})
	if err != nil {
		return WorkflowResult{}, err
	}

	type branchResult struct {
		index int
		task  core.Task
		err   error
	}
	results := make(chan branchResult, len(definition.Steps))
	var wait sync.WaitGroup
	for index, definitionStep := range definition.Steps {
		index, definitionStep := index, definitionStep
		wait.Add(1)
		go func() {
			defer wait.Done()
			task, submitErr := c.Service.SubmitTask(ctx, tenantID, hub.SubmitTaskInput{
				AgentID: definitionStep.AgentID, Skill: definitionStep.Skill, Text: definitionStep.Text,
			})
			results <- branchResult{index: index, task: task, err: submitErr}
		}()
	}
	wait.Wait()
	close(results)
	ordered := make([]branchResult, len(definition.Steps))
	for result := range results {
		ordered[result.index] = result
	}
	var failures []string
	for index, result := range ordered {
		stepID := steps[index].ID
		_, _, updateErr := store.ApplyWorkflowVersion(ctx, tenantID, workflow.ID, 0,
			"start:"+stepID, func(current *core.Workflow) (core.WorkflowEvent, error) {
				step := &current.Steps[index]
				if result.err != nil {
					step.State = core.TaskStateFailed
					problem := core.Problem{Category: "provider", Code: "STEP_SUBMIT_FAILED", Message: result.err.Error(), Retryable: true}
					step.Problem = &problem
					failures = append(failures, fmt.Sprintf("%s: %v", stepID, result.err))
				} else {
					step.TaskID = result.task.ID
					step.AgentID = result.task.AgentID
					step.State = result.task.State
				}
				current.State = workflowStateForSteps(current.Steps)
				current.UpdatedAt = time.Now().UTC()
				return core.WorkflowEvent{Type: "workflow.step.submitted", Source: "hub", StepID: stepID, State: current.State, CreatedAt: current.UpdatedAt}, nil
			})
		if updateErr != nil {
			return WorkflowResult{Workflow: workflow, Errors: failures}, updateErr
		}
	}
	workflow, err = store.GetWorkflow(ctx, tenantID, workflow.ID)
	return WorkflowResult{Workflow: workflow, Errors: failures}, err
}

// ReconcileWorkflow refreshes every non-terminal child Task and then advances
// the aggregate. It is safe to call after a process restart.
func (c *Coordinator) ReconcileWorkflow(ctx context.Context, tenantID, workflowID string, subscribe bool) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State == core.WorkflowStateCompensating {
		workflow, err = c.reconcileCompensation(ctx, tenantID, workflowID)
		return WorkflowResult{Workflow: workflow}, err
	}
	var failures []string
	for index := range workflow.Steps {
		step := workflow.Steps[index]
		if step.TaskID == "" {
			continue
		}
		task, reconcileErr := c.Service.ReconcileTask(ctx, tenantID, step.TaskID, subscribe)
		if reconcileErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", step.ID, reconcileErr))
			continue
		}
		workflow, err = c.updateStep(ctx, workflow, index, task, false)
		if err != nil {
			return WorkflowResult{Workflow: workflow, Errors: failures}, err
		}
	}
	workflow, err = store.GetWorkflow(ctx, tenantID, workflowID)
	return WorkflowResult{Workflow: workflow, Errors: failures}, err
}

// ContinueWorkflow resumes all waiting branches with the same user input.
func (c *Coordinator) ContinueWorkflow(ctx context.Context, tenantID, workflowID, text string) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State != core.WorkflowStateWaitingInput {
		return WorkflowResult{Workflow: workflow}, fmt.Errorf("workflow continuation requires WAITING_INPUT state, got %s", workflow.State)
	}
	var failures []string
	for index, step := range workflow.Steps {
		if step.State != core.TaskStateInputRequired || step.TaskID == "" {
			continue
		}
		task, continueErr := c.Service.ContinueTask(ctx, tenantID, step.TaskID, hub.ContinueTaskInput{Text: text})
		if continueErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", step.ID, continueErr))
			continue
		}
		workflow, err = c.updateStep(ctx, workflow, index, task, false)
		if err != nil {
			return WorkflowResult{Workflow: workflow, Errors: failures}, err
		}
	}
	workflow, err = store.GetWorkflow(ctx, tenantID, workflowID)
	return WorkflowResult{Workflow: workflow, Errors: failures}, err
}

// CompensateWorkflow submits explicit provider-owned compensating Tasks for
// completed steps. No hidden rollback is attempted: compensation is an
// observable, idempotent phase of the aggregate.
func (c *Coordinator) CompensateWorkflow(ctx context.Context, tenantID, workflowID string) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State != core.WorkflowStateFailed && workflow.State != core.WorkflowStatePartiallyFailed {
		return WorkflowResult{Workflow: workflow}, fmt.Errorf("workflow compensation requires failed state, got %s", workflow.State)
	}
	workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, "compensation:started", func(current *core.Workflow) (core.WorkflowEvent, error) {
		current.State = core.WorkflowStateCompensating
		current.UpdatedAt = time.Now().UTC()
		return core.WorkflowEvent{Type: "workflow.compensation.started", Source: "hub", State: current.State, CreatedAt: current.UpdatedAt}, nil
	})
	if err != nil {
		return WorkflowResult{}, err
	}
	var failures []string
	for index := range workflow.Steps {
		step := workflow.Steps[index]
		if step.State != core.TaskStateCompleted || step.CompensationText == "" || step.CompensationTaskID != "" {
			continue
		}
		task, submitErr := c.Service.SubmitTask(ctx, tenantID, hub.SubmitTaskInput{AgentID: step.AgentID, Text: step.CompensationText})
		if submitErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", step.ID, submitErr))
			_, _, _ = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, "compensation:"+step.ID, func(current *core.Workflow) (core.WorkflowEvent, error) {
				problem := core.Problem{Category: "provider", Code: "COMPENSATION_SUBMIT_FAILED", Message: submitErr.Error(), Retryable: true}
				current.Steps[index].CompensationState = core.TaskStateFailed
				current.Steps[index].CompensationProblem = &problem
				current.State = core.WorkflowStateFailed
				current.UpdatedAt = time.Now().UTC()
				return core.WorkflowEvent{Type: "workflow.compensation.failed", Source: "hub", StepID: step.ID, State: current.State, Problem: &problem, CreatedAt: current.UpdatedAt}, nil
			})
			continue
		}
		workflow, err = store.GetWorkflow(ctx, tenantID, workflowID)
		if err != nil {
			return WorkflowResult{}, err
		}
		workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, "compensation:"+step.ID, func(current *core.Workflow) (core.WorkflowEvent, error) {
			current.Steps[index].CompensationTaskID = task.ID
			current.Steps[index].CompensationState = task.State
			current.UpdatedAt = time.Now().UTC()
			return core.WorkflowEvent{Type: "workflow.compensation.submitted", Source: "hub", StepID: step.ID, State: current.State, CreatedAt: current.UpdatedAt}, nil
		})
		if err != nil {
			return WorkflowResult{Workflow: workflow, Errors: failures}, err
		}
	}
	workflow, err = c.reconcileCompensation(ctx, tenantID, workflowID)
	return WorkflowResult{Workflow: workflow, Errors: failures}, err
}

func (c *Coordinator) reconcileCompensation(ctx context.Context, tenantID, workflowID string) (core.Workflow, error) {
	store, err := c.workflowStore()
	if err != nil {
		return core.Workflow{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return core.Workflow{}, err
	}
	allDone, anyFailed := true, false
	for index, step := range workflow.Steps {
		if step.CompensationTaskID == "" {
			if step.CompensationState == core.TaskStateFailed || step.CompensationState == core.TaskStateRejected || step.CompensationState == core.TaskStateCanceled {
				anyFailed = true
				continue
			}
			if step.State == core.TaskStateCompleted && step.CompensationText != "" {
				allDone = false
			}
			continue
		}
		task, getErr := c.Service.GetTask(ctx, tenantID, step.CompensationTaskID)
		if getErr != nil {
			return workflow, getErr
		}
		if !task.State.Terminal() {
			allDone = false
		}
		if task.State == core.TaskStateFailed || task.State == core.TaskStateRejected || task.State == core.TaskStateCanceled {
			anyFailed = true
		}
		workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, "compensation:state:"+step.ID+":"+string(task.State), func(current *core.Workflow) (core.WorkflowEvent, error) {
			current.Steps[index].CompensationState = task.State
			current.UpdatedAt = time.Now().UTC()
			if task.Problem != nil {
				current.Steps[index].CompensationProblem = task.Problem
			}
			return core.WorkflowEvent{Type: "workflow.compensation.status", Source: "hub", StepID: step.ID, CreatedAt: current.UpdatedAt}, nil
		})
		if err != nil {
			return workflow, err
		}
	}
	if allDone {
		state := core.WorkflowStateCompensated
		if anyFailed {
			state = core.WorkflowStateFailed
		}
		// Include the aggregate revision so a later recovery generation is not
		// blocked by a terminal marker from an earlier compensation attempt.
		workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, fmt.Sprintf("compensation:terminal:%s:%d", state, workflow.Revision), func(current *core.Workflow) (core.WorkflowEvent, error) {
			current.State = state
			current.UpdatedAt = time.Now().UTC()
			return core.WorkflowEvent{Type: "workflow.compensation.terminal", Source: "hub", State: state, CreatedAt: current.UpdatedAt}, nil
		})
		if err != nil {
			return workflow, err
		}
	}
	return workflow, nil
}

func (c *Coordinator) updateStep(ctx context.Context, workflow core.Workflow, index int, task core.Task, compensation bool) (core.Workflow, error) {
	store, err := c.workflowStore()
	if err != nil {
		return workflow, err
	}
	updated, _, err := store.ApplyWorkflowVersion(ctx, workflow.TenantID, workflow.ID, 0,
		fmt.Sprintf("task:%s:%d:%s", workflow.Steps[index].ID, task.Revision, task.State), func(current *core.Workflow) (core.WorkflowEvent, error) {
			if compensation {
				current.Steps[index].CompensationState = task.State
			} else {
				current.Steps[index].TaskID = task.ID
				current.Steps[index].State = task.State
				current.Steps[index].Problem = task.Problem
				current.State = workflowStateForSteps(current.Steps)
			}
			current.UpdatedAt = time.Now().UTC()
			return core.WorkflowEvent{Type: "workflow.step.status", Source: "hub", StepID: current.Steps[index].ID, State: current.State, CreatedAt: current.UpdatedAt}, nil
		})
	return updated, err
}

func (c *Coordinator) workflowStore() (core.WorkflowStore, error) {
	if c == nil || c.Service == nil || c.Service.Store == nil {
		return nil, errors.New("coordinator service is required")
	}
	store, ok := c.Service.Store.(core.WorkflowStore)
	if !ok {
		return nil, errors.New("configured store does not support durable workflows")
	}
	return store, nil
}

func workflowStateForSteps(steps []core.WorkflowStep) core.WorkflowState {
	allTerminal, allCompleted, anyFailure, anyInput := true, true, false, false
	for _, step := range steps {
		if !step.State.Terminal() {
			allTerminal = false
		}
		if step.State != core.TaskStateCompleted {
			allCompleted = false
		}
		if step.State == core.TaskStateInputRequired {
			anyInput = true
		}
		if step.State == core.TaskStateFailed || step.State == core.TaskStateRejected || step.State == core.TaskStateCanceled {
			anyFailure = true
		}
	}
	if anyInput {
		return core.WorkflowStateWaitingInput
	}
	if allTerminal && allCompleted {
		return core.WorkflowStateCompleted
	}
	if allTerminal && anyFailure {
		for _, step := range steps {
			if step.State == core.TaskStateCompleted {
				return core.WorkflowStatePartiallyFailed
			}
		}
		return core.WorkflowStateFailed
	}
	return core.WorkflowStateRunning
}
