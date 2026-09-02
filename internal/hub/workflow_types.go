package hub

import (
	"context"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// WorkflowStepInput is the Hub-owned, provider-opaque description of one
// child Task. It contains no provider runtime-private state.
type WorkflowStepInput struct {
	ID               string   `json:"id,omitempty"`
	AgentID          string   `json:"agentId,omitempty"`
	Skill            string   `json:"skill,omitempty"`
	DependsOn        []string `json:"dependsOn,omitempty"`
	Text             string   `json:"text"`
	Required         bool     `json:"required"`
	CompensationText string   `json:"compensationText,omitempty"`
}

type WorkflowDefinition struct {
	ID                string              `json:"id,omitempty"`
	Name              string              `json:"name"`
	DefinitionVersion int                 `json:"definitionVersion,omitempty"`
	IdempotencyKey    string              `json:"idempotencyKey,omitempty"`
	MaxConcurrency    int                 `json:"maxConcurrency,omitempty"`
	Steps             []WorkflowStepInput `json:"steps"`
}

type WorkflowResult struct {
	Workflow core.Workflow `json:"workflow"`
	Errors   []string      `json:"errors,omitempty"`
}

// WorkflowCoordinator is the narrow management boundary used by HTTP. The
// concrete implementation remains replaceable and provider-opaque.
type WorkflowCoordinator interface {
	StartWorkflow(context.Context, string, WorkflowDefinition) (WorkflowResult, error)
	ReconcileWorkflow(context.Context, string, string, bool) (WorkflowResult, error)
	ContinueWorkflow(context.Context, string, string, string) (WorkflowResult, error)
	CompensateWorkflow(context.Context, string, string) (WorkflowResult, error)
	PauseWorkflow(context.Context, string, string) (WorkflowResult, error)
	ResumeWorkflow(context.Context, string, string) (WorkflowResult, error)
	CancelWorkflow(context.Context, string, string) (WorkflowResult, error)
	GetWorkflow(context.Context, string, string) (core.Workflow, error)
	ListWorkflows(context.Context, string) ([]core.Workflow, error)
}

type WorkerController interface {
	ModeString() string
	Pause()
	Resume()
	BeginDrain()
}
