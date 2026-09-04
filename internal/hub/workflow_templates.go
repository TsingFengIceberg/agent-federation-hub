package hub

import (
	"context"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

// WorkflowTemplate is a versioned, Hub-owned compilation recipe. It describes
// public dependency and Artifact-flow topology only; it is not a provider
// runtime workflow, prompt graph, or tool plan.
type WorkflowTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	MinAgents   int    `json:"minAgents"`
	MaxAgents   int    `json:"maxAgents"`
}

// WorkflowTemplateAgent binds one topology role to either a registered Agent
// or a skill that the Service will resolve in the caller's tenant.
type WorkflowTemplateAgent struct {
	AgentID string `json:"agentId,omitempty"`
	Skill   string `json:"skill,omitempty"`
}

// WorkflowTemplateRunInput supplies runtime routing and provider input to a
// fixed template. The Hub stores no provider-private workflow information.
type WorkflowTemplateRunInput struct {
	ID             string                  `json:"id,omitempty"`
	Name           string                  `json:"name,omitempty"`
	IdempotencyKey string                  `json:"idempotencyKey,omitempty"`
	MaxConcurrency int                     `json:"maxConcurrency,omitempty"`
	Text           string                  `json:"text,omitempty"`
	Parts          []core.Part             `json:"parts,omitempty"`
	Agents         []WorkflowTemplateAgent `json:"agents"`
}

// WorkflowTemplates is the small provider-opaque extension of the generic
// Workflow coordinator required by the management API.
type WorkflowTemplates interface {
	ListWorkflowTemplates() []WorkflowTemplate
	StartWorkflowTemplate(context.Context, string, string, WorkflowTemplateRunInput) (WorkflowResult, error)
}
