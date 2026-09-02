package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

type StepDefinition = hub.WorkflowStepInput
type WorkflowDefinition = hub.WorkflowDefinition
type WorkflowResult = hub.WorkflowResult

func normalizeDependencies(stepID string, values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		dependency := strings.TrimSpace(raw)
		if dependency == "" {
			return nil, fmt.Errorf("workflow step %q has an empty dependency", stepID)
		}
		if dependency == stepID {
			return nil, fmt.Errorf("workflow step %q cannot depend on itself", stepID)
		}
		if _, ok := seen[dependency]; ok {
			continue
		}
		seen[dependency] = struct{}{}
		result = append(result, dependency)
	}
	sort.Strings(result)
	return result, nil
}

func validateWorkflowGraph(steps []core.WorkflowStep) error {
	known := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		known[step.ID] = struct{}{}
	}
	graph := make(map[string][]string, len(steps))
	for _, step := range steps {
		for _, dependency := range step.DependsOn {
			if _, ok := known[dependency]; !ok {
				return fmt.Errorf("workflow step %q depends on unknown step %q", step.ID, dependency)
			}
			graph[step.ID] = append(graph[step.ID], dependency)
		}
	}
	visit := make(map[string]uint8, len(steps))
	var walk func(string) error
	walk = func(id string) error {
		switch visit[id] {
		case 1:
			return fmt.Errorf("workflow dependency cycle detected at step %q", id)
		case 2:
			return nil
		}
		visit[id] = 1
		for _, dependency := range graph[id] {
			if err := walk(dependency); err != nil {
				return err
			}
		}
		visit[id] = 2
		return nil
	}
	for _, step := range steps {
		if err := walk(step.ID); err != nil {
			return err
		}
	}
	return nil
}

// submitWorkflowReady advances only dependency-ready steps. A batch is
// bounded by maxConcurrency and each mutation is idempotent in the durable
// workflow store, so a retry after a process crash cannot resubmit a step that
// already has a local Task ID.
func (c *Coordinator) submitWorkflowReady(ctx context.Context, tenantID, workflowID string, maxConcurrency int, failures *[]string) error {
	store, err := c.workflowStore()
	if err != nil {
		return err
	}
	for {
		workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
		if err != nil {
			return err
		}
		ready := make([]int, 0, maxConcurrency)
		blocked := make([]int, 0)
		for index, step := range workflow.Steps {
			if step.TaskID != "" || step.State != core.TaskStateUnknown {
				continue
			}
			allTerminal, dependencyFailed := true, false
			for _, dependencyID := range step.DependsOn {
				dependency := findWorkflowStep(workflow.Steps, dependencyID)
				if dependency == nil || !dependency.State.Terminal() {
					allTerminal = false
					break
				}
				if dependency.State != core.TaskStateCompleted {
					dependencyFailed = true
				}
			}
			if !allTerminal {
				continue
			}
			if dependencyFailed && step.Required {
				blocked = append(blocked, index)
			} else {
				ready = append(ready, index)
			}
		}
		for _, index := range blocked {
			stepID := workflow.Steps[index].ID
			_, _, applyErr := store.ApplyWorkflowVersion(ctx, tenantID, workflow.ID, 0, "blocked:"+stepID, func(current *core.Workflow) (core.WorkflowEvent, error) {
				problem := core.Problem{Category: "state", Code: "DEPENDENCY_FAILED", Message: "a required workflow dependency failed"}
				current.Steps[index].State = core.TaskStateRejected
				current.Steps[index].Problem = &problem
				current.State = workflowStateForSteps(current.Steps)
				current.UpdatedAt = time.Now().UTC()
				return core.WorkflowEvent{Type: "workflow.step.blocked", Source: "hub", StepID: stepID, State: current.State, Problem: &problem, CreatedAt: current.UpdatedAt}, nil
			})
			if applyErr != nil {
				return applyErr
			}
			*failures = append(*failures, fmt.Sprintf("%s: required dependency failed", stepID))
		}
		if len(ready) == 0 {
			return nil
		}
		if len(ready) > maxConcurrency {
			ready = ready[:maxConcurrency]
		}
		type branchResult struct {
			index int
			task  core.Task
			err   error
		}
		results := make(chan branchResult, len(ready))
		for _, index := range ready {
			index := index
			step := workflow.Steps[index]
			go func() {
				text, inputErr := c.inputStore().Get(ctx, tenantID, step.InputRef)
				if inputErr != nil {
					results <- branchResult{index: index, err: inputErr}
					return
				}
				task, submitErr := c.Service.SubmitTask(ctx, tenantID, hub.SubmitTaskInput{AgentID: step.AgentID, Skill: step.Skill, Text: text})
				results <- branchResult{index: index, task: task, err: submitErr}
			}()
		}
		for range ready {
			result := <-results
			stepID := workflow.Steps[result.index].ID
			_, _, applyErr := store.ApplyWorkflowVersion(ctx, tenantID, workflow.ID, 0, "start:"+stepID, func(current *core.Workflow) (core.WorkflowEvent, error) {
				step := &current.Steps[result.index]
				step.Attempt++
				if result.err != nil {
					step.State = core.TaskStateFailed
					problem := core.Problem{Category: "provider", Code: "STEP_SUBMIT_FAILED", Message: result.err.Error(), Retryable: true}
					step.Problem = &problem
					*failures = append(*failures, fmt.Sprintf("%s: %v", stepID, result.err))
				} else {
					step.TaskID = result.task.ID
					step.AgentID = result.task.AgentID
					step.State = result.task.State
				}
				current.State = workflowStateForSteps(current.Steps)
				current.UpdatedAt = time.Now().UTC()
				return core.WorkflowEvent{Type: "workflow.step.submitted", Source: "hub", StepID: stepID, State: current.State, CreatedAt: current.UpdatedAt}, nil
			})
			if applyErr != nil {
				return applyErr
			}
		}
	}
}

func findWorkflowStep(steps []core.WorkflowStep, id string) *core.WorkflowStep {
	for index := range steps {
		if steps[index].ID == id {
			return &steps[index]
		}
	}
	return nil
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
	definitionVersion := definition.DefinitionVersion
	if definitionVersion <= 0 {
		definitionVersion = 1
	}
	if definitionVersion > 1000 {
		return WorkflowResult{}, errors.New("workflow definition version must be between 1 and 1000")
	}
	maxConcurrency := definition.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = len(definition.Steps)
	}
	if maxConcurrency < 1 || maxConcurrency > 1024 {
		return WorkflowResult{}, errors.New("workflow maxConcurrency must be between 1 and 1024")
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
		dependencies, dependencyErr := normalizeDependencies(stepID, definitionStep.DependsOn)
		if dependencyErr != nil {
			return WorkflowResult{}, dependencyErr
		}
		steps = append(steps, core.WorkflowStep{
			ID: stepID, AgentID: definitionStep.AgentID, Skill: definitionStep.Skill,
			DependsOn: dependencies,
			State:     core.TaskStateUnknown, Required: definitionStep.Required,
			CompensationText: definitionStep.CompensationText,
		})
	}
	if err := validateWorkflowGraph(steps); err != nil {
		return WorkflowResult{}, err
	}
	workflowID := definition.ID
	if workflowID == "" {
		if strings.TrimSpace(definition.IdempotencyKey) != "" {
			workflowID = "wf-" + core.DigestString(tenantID + "\x00" + strings.TrimSpace(definition.IdempotencyKey))[:32]
		} else {
			workflowID = core.NewID()
		}
	}
	now := time.Now().UTC()
	workflow, err := store.CreateWorkflow(ctx, core.Workflow{
		ID: workflowID, TenantID: tenantID, Name: definition.Name,
		DefinitionVersion: definitionVersion, IdempotencyKey: definition.IdempotencyKey,
		MaxConcurrency: maxConcurrency,
		State:          core.WorkflowStatePending, Steps: steps, CreatedAt: now, UpdatedAt: now,
	}, core.WorkflowEvent{Type: "workflow.created", Source: "hub", State: core.WorkflowStatePending, CreatedAt: now})
	if err != nil {
		if errors.Is(err, core.ErrConflict) && strings.TrimSpace(definition.IdempotencyKey) != "" {
			existing, getErr := store.GetWorkflow(ctx, tenantID, workflowID)
			if getErr == nil && existing.IdempotencyKey == strings.TrimSpace(definition.IdempotencyKey) && existing.DefinitionVersion == definitionVersion {
				return WorkflowResult{Workflow: existing}, nil
			}
		}
		return WorkflowResult{}, err
	}

	inputStore := c.inputStore()
	for index, definitionStep := range definition.Steps {
		ref, inputErr := inputStore.Put(ctx, tenantID, workflow.ID, steps[index].ID, definitionStep.Text)
		if inputErr != nil {
			return WorkflowResult{Workflow: workflow}, inputErr
		}
		_, _, inputRefErr := store.ApplyWorkflowVersion(ctx, tenantID, workflow.ID, 0, "input:"+steps[index].ID, func(current *core.Workflow) (core.WorkflowEvent, error) {
			current.Steps[index].InputRef = ref
			current.Steps[index].InputDigest = core.DigestString(definitionStep.Text)
			current.UpdatedAt = time.Now().UTC()
			return core.WorkflowEvent{Type: "workflow.input.stored", Source: "hub", StepID: steps[index].ID, CreatedAt: current.UpdatedAt}, nil
		})
		if inputRefErr != nil {
			return WorkflowResult{Workflow: workflow}, inputRefErr
		}
	}
	var failures []string
	if err := c.submitWorkflowReady(ctx, tenantID, workflow.ID, maxConcurrency, &failures); err != nil {
		return WorkflowResult{Workflow: workflow, Errors: failures}, err
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
	if workflow.State == core.WorkflowStatePaused {
		return WorkflowResult{Workflow: workflow}, nil
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
	maxConcurrency := workflow.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = len(workflow.Steps)
	}
	if err := c.submitWorkflowReady(ctx, tenantID, workflowID, maxConcurrency, &failures); err != nil {
		return WorkflowResult{Workflow: workflow, Errors: failures}, err
	}
	workflow, err = store.GetWorkflow(ctx, tenantID, workflowID)
	return WorkflowResult{Workflow: workflow, Errors: failures}, err
}

func (c *Coordinator) GetWorkflow(ctx context.Context, tenantID, workflowID string) (core.Workflow, error) {
	store, err := c.workflowStore()
	if err != nil {
		return core.Workflow{}, err
	}
	return store.GetWorkflow(ctx, tenantID, workflowID)
}

func (c *Coordinator) ListWorkflows(ctx context.Context, tenantID string) ([]core.Workflow, error) {
	store, err := c.workflowStore()
	if err != nil {
		return nil, err
	}
	return store.ListWorkflows(ctx, tenantID)
}

func (c *Coordinator) PauseWorkflow(ctx context.Context, tenantID, workflowID string) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State.Terminal() || workflow.State == core.WorkflowStatePaused {
		return WorkflowResult{Workflow: workflow}, fmt.Errorf("workflow cannot be paused from state %s", workflow.State)
	}
	dedupKey := fmt.Sprintf("workflow:pause:%d", workflow.Revision)
	workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, dedupKey, func(current *core.Workflow) (core.WorkflowEvent, error) {
		if current.State.Terminal() || current.State == core.WorkflowStatePaused {
			return core.WorkflowEvent{}, fmt.Errorf("workflow cannot be paused from state %s", current.State)
		}
		current.PausedFrom = current.State
		current.State = core.WorkflowStatePaused
		current.UpdatedAt = time.Now().UTC()
		return core.WorkflowEvent{Type: "workflow.paused", Source: "operator", State: current.State, CreatedAt: current.UpdatedAt}, nil
	})
	return WorkflowResult{Workflow: workflow}, err
}

func (c *Coordinator) ResumeWorkflow(ctx context.Context, tenantID, workflowID string) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State != core.WorkflowStatePaused {
		return WorkflowResult{Workflow: workflow}, fmt.Errorf("workflow cannot be resumed from state %s", workflow.State)
	}
	dedupKey := fmt.Sprintf("workflow:resume:%d", workflow.Revision)
	workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, dedupKey, func(current *core.Workflow) (core.WorkflowEvent, error) {
		if current.State != core.WorkflowStatePaused {
			return core.WorkflowEvent{}, fmt.Errorf("workflow cannot be resumed from state %s", current.State)
		}
		current.State = workflowStateForSteps(current.Steps)
		current.PausedFrom = ""
		current.UpdatedAt = time.Now().UTC()
		return core.WorkflowEvent{Type: "workflow.resumed", Source: "operator", State: current.State, CreatedAt: current.UpdatedAt}, nil
	})
	return WorkflowResult{Workflow: workflow}, err
}

func (c *Coordinator) CancelWorkflow(ctx context.Context, tenantID, workflowID string) (WorkflowResult, error) {
	store, err := c.workflowStore()
	if err != nil {
		return WorkflowResult{}, err
	}
	workflow, err := store.GetWorkflow(ctx, tenantID, workflowID)
	if err != nil {
		return WorkflowResult{}, err
	}
	if workflow.State.Terminal() {
		return WorkflowResult{Workflow: workflow}, fmt.Errorf("workflow is already terminal: %s", workflow.State)
	}
	var failures []string
	for _, step := range workflow.Steps {
		if step.TaskID == "" || step.State.Terminal() {
			continue
		}
		if _, cancelErr := c.Service.CancelTask(ctx, tenantID, step.TaskID); cancelErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", step.ID, cancelErr))
		}
	}
	dedupKey := fmt.Sprintf("workflow:cancel:%d", workflow.Revision)
	workflow, _, err = store.ApplyWorkflowVersion(ctx, tenantID, workflowID, 0, dedupKey, func(current *core.Workflow) (core.WorkflowEvent, error) {
		current.State = core.WorkflowStateCanceled
		current.UpdatedAt = time.Now().UTC()
		return core.WorkflowEvent{Type: "workflow.canceled", Source: "operator", State: current.State, CreatedAt: current.UpdatedAt}, nil
	})
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
	maxConcurrency := workflow.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = len(workflow.Steps)
	}
	if err := c.submitWorkflowReady(ctx, tenantID, workflowID, maxConcurrency, &failures); err != nil {
		return WorkflowResult{Workflow: workflow, Errors: failures}, err
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
