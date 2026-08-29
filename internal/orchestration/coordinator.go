// Package orchestration owns Hub-level coordination across opaque Agents. It
// is intentionally separate from provider-internal runtimes such as
// LangGraph: only A2A-visible Tasks, Artifacts and input-required states cross
// this boundary.
package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

type FanoutInput struct {
	AgentID string `json:"agentId,omitempty"`
	Skill   string `json:"skill,omitempty"`
	Text    string `json:"text"`
}

type FanoutResult struct {
	Tasks    []core.Task `json:"tasks"`
	Failures []string    `json:"failures,omitempty"`
}

type Coordinator struct {
	Service *hub.Service
}

// FanOut submits independent A2A Tasks concurrently. A failure in one Agent
// does not cancel other branches; callers can inspect the per-branch Task or
// failure and decide whether compensation is needed.
func (c *Coordinator) FanOut(ctx context.Context, tenantID string, inputs []FanoutInput) (FanoutResult, error) {
	if c == nil || c.Service == nil {
		return FanoutResult{}, errors.New("coordinator service is required")
	}
	if len(inputs) == 0 {
		return FanoutResult{}, errors.New("at least one fan-out input is required")
	}
	type result struct {
		index int
		task  core.Task
		err   error
	}
	results := make(chan result, len(inputs))
	var wait sync.WaitGroup
	for index, input := range inputs {
		index, input := index, input
		wait.Add(1)
		go func() {
			defer wait.Done()
			task, err := c.Service.SubmitTask(ctx, tenantID, hub.SubmitTaskInput{AgentID: input.AgentID, Skill: input.Skill, Text: input.Text})
			results <- result{index: index, task: task, err: err}
		}()
	}
	wait.Wait()
	close(results)
	ordered := make([]result, len(inputs))
	for item := range results {
		ordered[item.index] = item
	}
	response := FanoutResult{Tasks: make([]core.Task, 0, len(inputs))}
	for index, item := range ordered {
		if item.err != nil {
			response.Failures = append(response.Failures, fmt.Sprintf("branch[%d]: %v", index, item.err))
			continue
		}
		response.Tasks = append(response.Tasks, item.task)
	}
	if len(response.Tasks) == 0 {
		return response, errors.New("all fan-out branches failed")
	}
	return response, nil
}

// FanIn returns the latest states of all branches. It is a read-side
// aggregation primitive; it never hides partial failures or silently retries
// provider work.
func (c *Coordinator) FanIn(ctx context.Context, tenantID string, taskIDs []string) (FanoutResult, error) {
	if c == nil || c.Service == nil {
		return FanoutResult{}, errors.New("coordinator service is required")
	}
	if len(taskIDs) == 0 {
		return FanoutResult{}, errors.New("at least one task ID is required")
	}
	response := FanoutResult{Tasks: make([]core.Task, 0, len(taskIDs))}
	for index, taskID := range taskIDs {
		task, err := c.Service.GetTask(ctx, tenantID, taskID)
		if err != nil {
			response.Failures = append(response.Failures, fmt.Sprintf("branch[%d] %s: %v", index, taskID, err))
			continue
		}
		response.Tasks = append(response.Tasks, task)
	}
	if len(response.Tasks) == 0 {
		return response, errors.New("no fan-in branch could be read")
	}
	return response, nil
}

// ContinueAfterApproval is the explicit human-in-the-loop boundary. The Hub
// only resumes a provider Task in INPUT_REQUIRED state and preserves its
// remote Task/Context IDs through Service.ContinueTask.
func (c *Coordinator) ContinueAfterApproval(ctx context.Context, tenantID, taskID, text string) (core.Task, error) {
	if c == nil || c.Service == nil {
		return core.Task{}, errors.New("coordinator service is required")
	}
	return c.Service.ContinueTask(ctx, tenantID, taskID, hub.ContinueTaskInput{Text: text})
}
