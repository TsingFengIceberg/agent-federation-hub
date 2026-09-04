package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

var workflowTemplates = []hub.WorkflowTemplate{
	{ID: "single-agent", Name: "Single Agent", Description: "Run one opaque A2A Provider Task.", Version: 1, MinAgents: 1, MaxAgents: 1},
	{ID: "sequential-pipeline", Name: "Sequential Pipeline", Description: "Run stages in order and project observed A2A Artifacts to the next stage.", Version: 1, MinAgents: 2, MaxAgents: 64},
	{ID: "parallel-fanout", Name: "Parallel Fan-out", Description: "Run independent Provider Tasks concurrently with the same input.", Version: 1, MinAgents: 2, MaxAgents: 64},
	{ID: "review-revision", Name: "Review Revision", Description: "Send draft output to a reviewer, then return observed review Artifacts to the original Provider.", Version: 1, MinAgents: 2, MaxAgents: 2},
	{ID: "human-approval", Name: "Human Approval", Description: "Run one Provider Task and wait only when that Provider reports INPUT_REQUIRED.", Version: 1, MinAgents: 1, MaxAgents: 1},
}

func (c *Coordinator) ListWorkflowTemplates() []hub.WorkflowTemplate {
	result := make([]hub.WorkflowTemplate, len(workflowTemplates))
	copy(result, workflowTemplates)
	return result
}

func (c *Coordinator) StartWorkflowTemplate(ctx context.Context, tenantID, templateID string, input hub.WorkflowTemplateRunInput) (hub.WorkflowResult, error) {
	definition, err := CompileWorkflowTemplate(templateID, input)
	if err != nil {
		return hub.WorkflowResult{}, err
	}
	return c.StartWorkflow(ctx, tenantID, definition)
}

// CompileWorkflowTemplate turns a small, versioned topology catalog into the
// generic WorkflowDefinition used by every other Hub caller. It has no access
// to a Provider runtime and only declares observable Task and Artifact edges.
func CompileWorkflowTemplate(templateID string, input hub.WorkflowTemplateRunInput) (hub.WorkflowDefinition, error) {
	templateID = strings.TrimSpace(templateID)
	if _, err := hub.NormalizeMessageParts(input.Text, input.Parts); err != nil {
		return hub.WorkflowDefinition{}, fmt.Errorf("template input: %w", err)
	}
	template, ok := findWorkflowTemplate(templateID)
	if !ok {
		return hub.WorkflowDefinition{}, fmt.Errorf("unknown workflow template %q", templateID)
	}
	if len(input.Agents) < template.MinAgents || len(input.Agents) > template.MaxAgents {
		return hub.WorkflowDefinition{}, fmt.Errorf("workflow template %q requires between %d and %d agents", templateID, template.MinAgents, template.MaxAgents)
	}
	for index, agent := range input.Agents {
		if (strings.TrimSpace(agent.AgentID) == "") == (strings.TrimSpace(agent.Skill) == "") {
			return hub.WorkflowDefinition{}, fmt.Errorf("workflow template %q agent %d requires exactly one of agentId or skill", templateID, index+1)
		}
	}
	definition := hub.WorkflowDefinition{
		ID: input.ID, Name: strings.TrimSpace(input.Name), DefinitionVersion: template.Version,
		IdempotencyKey: input.IdempotencyKey, MaxConcurrency: input.MaxConcurrency,
	}
	if definition.Name == "" {
		definition.Name = templateID
	}
	step := func(id string, agent hub.WorkflowTemplateAgent, dependsOn []string, artifacts []core.WorkflowArtifactInput) hub.WorkflowStepInput {
		return hub.WorkflowStepInput{
			ID: id, AgentID: strings.TrimSpace(agent.AgentID), Skill: strings.TrimSpace(agent.Skill),
			DependsOn: dependsOn, Text: input.Text, Parts: input.Parts, ArtifactInputs: artifacts, Required: true,
		}
	}
	switch templateID {
	case "single-agent":
		definition.Steps = []hub.WorkflowStepInput{step("agent", input.Agents[0], nil, nil)}
	case "human-approval":
		definition.Steps = []hub.WorkflowStepInput{step("approval", input.Agents[0], nil, nil)}
	case "parallel-fanout":
		for index, agent := range input.Agents {
			definition.Steps = append(definition.Steps, step(fmt.Sprintf("branch-%d", index+1), agent, nil, nil))
		}
	case "sequential-pipeline":
		for index, agent := range input.Agents {
			id := fmt.Sprintf("stage-%d", index+1)
			if index == 0 {
				definition.Steps = append(definition.Steps, step(id, agent, nil, nil))
				continue
			}
			previous := fmt.Sprintf("stage-%d", index)
			definition.Steps = append(definition.Steps, step(id, agent, []string{previous}, []core.WorkflowArtifactInput{{FromStepID: previous}}))
		}
	case "review-revision":
		definition.Steps = []hub.WorkflowStepInput{
			step("draft", input.Agents[0], nil, nil),
			step("review", input.Agents[1], []string{"draft"}, []core.WorkflowArtifactInput{{FromStepID: "draft"}}),
			step("revision", input.Agents[0], []string{"review"}, []core.WorkflowArtifactInput{{FromStepID: "review"}}),
		}
	default:
		return hub.WorkflowDefinition{}, errors.New("workflow template compiler is incomplete")
	}
	return definition, nil
}

func findWorkflowTemplate(id string) (hub.WorkflowTemplate, bool) {
	for _, template := range workflowTemplates {
		if template.ID == id {
			return template, true
		}
	}
	return hub.WorkflowTemplate{}, false
}
