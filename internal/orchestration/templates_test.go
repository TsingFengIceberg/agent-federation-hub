package orchestration

import (
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

func TestCompileWorkflowTemplatesIntoProviderOpaqueDAGs(t *testing.T) {
	input := hub.WorkflowTemplateRunInput{
		Text:   "produce a release plan",
		Parts:  []core.Part{{Kind: core.PartData, Data: map[string]any{"audience": "engineering"}}},
		Agents: []hub.WorkflowTemplateAgent{{AgentID: "writer"}, {Skill: "review"}, {AgentID: "publisher"}},
	}
	pipeline, err := CompileWorkflowTemplate("sequential-pipeline", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipeline.Steps) != 3 || pipeline.Steps[1].DependsOn[0] != "stage-1" || len(pipeline.Steps[1].ArtifactInputs) != 1 || pipeline.Steps[1].ArtifactInputs[0].FromStepID != "stage-1" {
		t.Fatalf("pipeline=%+v", pipeline)
	}
	parallel, err := CompileWorkflowTemplate("parallel-fanout", input)
	if err != nil || len(parallel.Steps) != 3 || len(parallel.Steps[0].DependsOn) != 0 {
		t.Fatalf("parallel=%+v err=%v", parallel, err)
	}
	review, err := CompileWorkflowTemplate("review-revision", hub.WorkflowTemplateRunInput{Text: "review", Agents: input.Agents[:2]})
	if err != nil || len(review.Steps) != 3 || review.Steps[2].AgentID != "writer" || review.Steps[2].ArtifactInputs[0].FromStepID != "review" {
		t.Fatalf("review=%+v err=%v", review, err)
	}
	approval, err := CompileWorkflowTemplate("human-approval", hub.WorkflowTemplateRunInput{Text: "request", Agents: input.Agents[:1]})
	if err != nil || len(approval.Steps) != 1 || approval.Steps[0].ID != "approval" {
		t.Fatalf("approval=%+v err=%v", approval, err)
	}
}

func TestCompileWorkflowTemplateRejectsInvalidRouting(t *testing.T) {
	if _, err := CompileWorkflowTemplate("parallel-fanout", hub.WorkflowTemplateRunInput{Text: "x", Agents: []hub.WorkflowTemplateAgent{{AgentID: "one"}}}); err == nil {
		t.Fatal("fan-out accepted too few agents")
	}
	if _, err := CompileWorkflowTemplate("single-agent", hub.WorkflowTemplateRunInput{Text: "x", Agents: []hub.WorkflowTemplateAgent{{AgentID: "one", Skill: "also"}}}); err == nil {
		t.Fatal("template accepted ambiguous routing")
	}
	if _, err := CompileWorkflowTemplate("unknown", hub.WorkflowTemplateRunInput{Text: "x", Agents: []hub.WorkflowTemplateAgent{{AgentID: "one"}}}); err == nil {
		t.Fatal("unknown template accepted")
	}
}
