package hub

import (
	"context"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// WorkflowStepInput is the Hub-owned, provider-opaque description of one
// child Task. It contains no provider runtime-private state.
type WorkflowStepInput struct {
	ID               string                       `json:"id,omitempty"`
	AgentID          string                       `json:"agentId,omitempty"`
	Skill            string                       `json:"skill,omitempty"`
	DependsOn        []string                     `json:"dependsOn,omitempty"`
	Text             string                       `json:"text,omitempty"`
	Parts            []core.Part                  `json:"parts,omitempty"`
	ArtifactInputs   []core.WorkflowArtifactInput `json:"artifactInputs,omitempty"`
	Required         bool                         `json:"required"`
	CompensationText string                       `json:"compensationText,omitempty"`
}

// WorkflowContinueInput is the same user-controlled content contract used by
// an individual Task continuation. The Hub applies it only to existing
// INPUT_REQUIRED or AUTH_REQUIRED child Tasks; it does not create a
// replacement Provider Task or accept credential material.
type WorkflowContinueInput struct {
	Text  string      `json:"text,omitempty"`
	Parts []core.Part `json:"parts,omitempty"`
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
	ContinueWorkflow(context.Context, string, string, WorkflowContinueInput) (WorkflowResult, error)
	CompensateWorkflow(context.Context, string, string) (WorkflowResult, error)
	PauseWorkflow(context.Context, string, string) (WorkflowResult, error)
	ResumeWorkflow(context.Context, string, string) (WorkflowResult, error)
	CancelWorkflow(context.Context, string, string) (WorkflowResult, error)
	GetWorkflow(context.Context, string, string) (core.Workflow, error)
	ListWorkflows(context.Context, string) ([]core.Workflow, error)
	WorkflowTemplates
}

type WorkerController interface {
	ModeString() string
	Pause()
	Resume()
	BeginDrain()
}
