package orchestration

import (
	"context"
	"errors"
	"iter"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

type workflowAdapter struct{}

type runningWorkflowAdapter struct{}

type schedulingAdapter struct {
	mu     sync.Mutex
	starts []string
	active int
	max    int
}

func (a *schedulingAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{Name: "scheduler", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Endpoint: "http://127.0.0.1:1/a2a"}, nil
}
func (a *schedulingAdapter) Send(_ context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		a.mu.Lock()
		a.starts = append(a.starts, message.Text)
		a.active++
		if a.active > a.max {
			a.max = a.active
		}
		a.mu.Unlock()
		time.Sleep(time.Millisecond)
		a.mu.Lock()
		a.active--
		a.mu.Unlock()
		yield(federation.Observation{Source: agent.ID, RemoteTaskID: "remote-" + message.ID, RemoteContextID: "context-" + message.ID, State: core.TaskStateCompleted, Final: true}, nil)
	}
}
func (*schedulingAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCompleted, Final: true}, nil
}
func (*schedulingAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCanceled, Final: true}, nil
}
func (*schedulingAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateCompleted, Final: true}, nil)
	}
}

func (runningWorkflowAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{Name: "running-fixture", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Endpoint: "http://127.0.0.1:1/a2a", Streaming: true}, nil
}
func (runningWorkflowAdapter) Send(_ context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{Source: agent.ID, RemoteTaskID: "remote-" + message.ID, RemoteContextID: "context-" + message.ID, State: core.TaskStateWorking}, nil)
	}
}
func (runningWorkflowAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateWorking}, nil
}
func (runningWorkflowAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCanceled, Final: true}, nil
}
func (runningWorkflowAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateWorking}, nil)
	}
}

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

func TestWorkflowValidatesDependenciesAndHonorsConcurrencyBound(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		if err := store.PutAgent(context.Background(), core.Agent{ID: id, TenantID: "tenant", Name: id, HealthStatus: core.AgentHealthHealthy}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &schedulingAdapter{}
	coordinator := &Coordinator{Service: &hub.Service{Store: store, Adapter: adapter}}
	result, err := coordinator.StartWorkflow(context.Background(), "tenant", WorkflowDefinition{
		ID: "wf-deps", DefinitionVersion: 2, MaxConcurrency: 1,
		Steps: []StepDefinition{
			{ID: "a", AgentID: "agent-a", Text: "first", Required: true},
			{ID: "b", AgentID: "agent-b", Text: "second", Required: true, DependsOn: []string{"a"}},
			{ID: "c", AgentID: "agent-c", Text: "third", Required: true},
		},
	})
	if err != nil || result.Workflow.State != core.WorkflowStateCompleted {
		t.Fatalf("result=%+v err=%v", result.Workflow, err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.max > 1 {
		t.Fatalf("max concurrent calls=%d", adapter.max)
	}
	if len(adapter.starts) != 3 || adapter.starts[0] != "first" || adapter.starts[1] != "second" {
		t.Fatalf("submission order=%v", adapter.starts)
	}
	if _, err := coordinator.StartWorkflow(context.Background(), "tenant", WorkflowDefinition{ID: "wf-cycle", Steps: []StepDefinition{{ID: "a", AgentID: "agent-a", Text: "a", DependsOn: []string{"b"}}, {ID: "b", AgentID: "agent-b", Text: "b", DependsOn: []string{"a"}}}}); err == nil {
		t.Fatal("dependency cycle accepted")
	}
}

func TestWorkflowIdempotencyKeyReturnsExistingAggregate(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutAgent(context.Background(), core.Agent{ID: "agent", TenantID: "tenant", Name: "agent", HealthStatus: core.AgentHealthHealthy}); err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{Service: &hub.Service{Store: store, Adapter: workflowAdapter{}}}
	definition := WorkflowDefinition{IdempotencyKey: "request-42", Name: "once", Steps: []StepDefinition{{AgentID: "agent", Text: "work"}}}
	first, err := coordinator.StartWorkflow(context.Background(), "tenant", definition)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.StartWorkflow(context.Background(), "tenant", definition)
	if err != nil {
		t.Fatal(err)
	}
	if first.Workflow.ID != second.Workflow.ID || first.Workflow.Revision != second.Workflow.Revision {
		t.Fatalf("first=%+v second=%+v", first.Workflow, second.Workflow)
	}
}

func TestWorkflowOperatorPauseResumeAndCancel(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutAgent(context.Background(), core.Agent{ID: "agent", TenantID: "tenant", Name: "agent", HealthStatus: core.AgentHealthHealthy}); err != nil {
		t.Fatal(err)
	}
	coordinator := &Coordinator{Service: &hub.Service{Store: store, Adapter: runningWorkflowAdapter{}}}
	started, err := coordinator.StartWorkflow(context.Background(), "tenant", WorkflowDefinition{ID: "wf-control", Name: "operator-control", Steps: []StepDefinition{{ID: "step", AgentID: "agent", Text: "long task"}}})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := coordinator.PauseWorkflow(context.Background(), "tenant", started.Workflow.ID)
	if err != nil || paused.Workflow.State != core.WorkflowStatePaused || paused.Workflow.PausedFrom == "" {
		t.Fatalf("paused=%+v err=%v", paused.Workflow, err)
	}
	blocked, err := coordinator.ReconcileWorkflow(context.Background(), "tenant", started.Workflow.ID, false)
	if err != nil || blocked.Workflow.State != core.WorkflowStatePaused {
		t.Fatalf("reconcile while paused=%+v err=%v", blocked.Workflow, err)
	}
	resumed, err := coordinator.ResumeWorkflow(context.Background(), "tenant", started.Workflow.ID)
	if err != nil || resumed.Workflow.State == core.WorkflowStatePaused {
		t.Fatalf("resumed=%+v err=%v", resumed.Workflow, err)
	}
	started, err = coordinator.StartWorkflow(context.Background(), "tenant", WorkflowDefinition{ID: "wf-cancel", Name: "operator-cancel", Steps: []StepDefinition{{ID: "step", AgentID: "agent", Text: "long task"}}})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := coordinator.CancelWorkflow(context.Background(), "tenant", started.Workflow.ID)
	if err != nil || canceled.Workflow.State != core.WorkflowStateCanceled {
		t.Fatalf("canceled=%+v err=%v", canceled.Workflow, err)
	}
}

type taskOnlyStore struct{ core.Store }
