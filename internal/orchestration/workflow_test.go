package orchestration

import (
	"context"
	"errors"
	"iter"
	"path/filepath"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

type workflowAdapter struct{}

func (workflowAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{Name: "workflow-fixture", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Endpoint: "http://127.0.0.1:1/a2a", Streaming: true}, nil
}

func (workflowAdapter) Send(_ context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		if agent.ID == "comp-failing" && message.Text == "release" {
			yield(federation.Observation{}, errors.New("simulated compensation rejection"))
			return
		}
		if agent.ID == "failing" && message.RemoteTaskID == "" {
			yield(federation.Observation{}, errors.New("simulated provider outage"))
			return
		}
		yield(federation.Observation{
			Source: agent.ID, RemoteTaskID: "remote-" + message.ID, RemoteContextID: "context-" + message.ID,
			State: core.TaskStateCompleted, Final: true,
		}, nil)
	}
}

func (workflowAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCompleted, Final: true}, nil
}
func (workflowAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCanceled, Final: true}, nil
}
func (workflowAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateCompleted, Final: true}, nil)
	}
}

func TestWorkflowPersistsPartialFailureAndCompensation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.journal")
	store, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"successful", "comp-failing", "failing"} {
		if err := store.PutAgent(context.Background(), core.Agent{ID: id, TenantID: "tenant-a", Name: id, HealthStatus: core.AgentHealthHealthy}); err != nil {
			t.Fatal(err)
		}
	}
	coordinator := &Coordinator{Service: &hub.Service{Store: store, Adapter: workflowAdapter{}}}
	result, err := coordinator.StartWorkflow(context.Background(), "tenant-a", WorkflowDefinition{
		ID: "wf-1", Name: "order-with-compensation",
		Steps: []StepDefinition{
			{ID: "reserve", AgentID: "successful", Text: "reserve", Required: true, CompensationText: "release"},
			{ID: "charge", AgentID: "failing", Text: "charge", Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.State != core.WorkflowStatePartiallyFailed {
		t.Fatalf("workflow state = %s, want PARTIALLY_FAILED", result.Workflow.State)
	}
	compensated, err := coordinator.CompensateWorkflow(context.Background(), "tenant-a", "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	if compensated.Workflow.State != core.WorkflowStateCompensated {
		t.Fatalf("compensated state = %s, want COMPENSATED", compensated.Workflow.State)
	}
	if compensated.Workflow.Steps[0].CompensationTaskID == "" || compensated.Workflow.Steps[0].CompensationState != core.TaskStateCompleted {
		t.Fatalf("compensation step = %+v", compensated.Workflow.Steps[0])
	}
	// Simulate a process restart while the compensation phase is still marked
	// active. ReconcileWorkflow must resume the compensation child and close
	// the aggregate instead of treating COMPENSATING as a dead end.
	if _, _, err := store.ApplyWorkflowVersion(context.Background(), "tenant-a", "wf-1", 0, "test:compensation-restart", func(current *core.Workflow) (core.WorkflowEvent, error) {
		current.State = core.WorkflowStateCompensating
		return core.WorkflowEvent{Type: "workflow.test.restarted", State: current.State}, nil
	}); err != nil {
		t.Fatal(err)
	}
	reconciled, err := coordinator.ReconcileWorkflow(context.Background(), "tenant-a", "wf-1", false)
	if err != nil || reconciled.Workflow.State != core.WorkflowStateCompensated {
		t.Fatalf("reconciled compensation=%+v err=%v", reconciled.Workflow, err)
	}
	failedCompensation, err := coordinator.StartWorkflow(context.Background(), "tenant-a", WorkflowDefinition{
		ID: "wf-failed-compensation", Name: "failed-compensation",
		Steps: []StepDefinition{
			{ID: "reserve", AgentID: "comp-failing", Text: "reserve", Required: true, CompensationText: "release"},
			{ID: "charge", AgentID: "failing", Text: "charge", Required: true},
		},
	})
	if err != nil || failedCompensation.Workflow.State != core.WorkflowStatePartiallyFailed {
		t.Fatalf("failed compensation setup=%+v err=%v", failedCompensation.Workflow, err)
	}
	failedResult, err := coordinator.CompensateWorkflow(context.Background(), "tenant-a", "wf-failed-compensation")
	if err != nil {
		t.Fatal(err)
	}
	if failedResult.Workflow.State != core.WorkflowStateFailed || failedResult.Workflow.Steps[0].CompensationState != core.TaskStateFailed {
		t.Fatalf("failed compensation state=%+v", failedResult.Workflow)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.GetWorkflow(context.Background(), "tenant-a", "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != core.WorkflowStateCompensated || persisted.Revision < 3 {
		t.Fatalf("persisted workflow = %+v", persisted)
	}
}

func TestWorkflowRequiresDurableStore(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A test double that only implements the task Store must not silently lose
	// workflow state; the coordinator rejects it at the boundary.
	if _, err := (&Coordinator{Service: &hub.Service{Store: taskOnlyStore{Store: store}, Adapter: workflowAdapter{}}}).StartWorkflow(context.Background(), "tenant", WorkflowDefinition{Steps: []StepDefinition{{AgentID: "a", Text: "x"}}}); err == nil {
		t.Fatal("expected durable workflow store requirement")
	}
}

type taskOnlyStore struct{ core.Store }
