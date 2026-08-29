package orchestration

import (
	"context"
	"iter"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/hub"
)

type completedAdapter struct{}

func (completedAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{Name: "fixture", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0", Endpoint: "https://agent.example"}, nil
}
func (completedAdapter) Send(_ context.Context, agent core.Agent, message federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{Source: agent.ID, RemoteTaskID: "remote-" + message.ID, RemoteContextID: "context-" + message.ID, State: core.TaskStateCompleted, Final: true}, nil)
	}
}
func (completedAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCompleted, Final: true}, nil
}
func (completedAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCanceled, Final: true}, nil
}
func (completedAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateCompleted, Final: true}, nil)
	}
}

func TestFanOutAndFanInPreservePartialBranches(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"agent-a", "agent-b"} {
		if err := store.PutAgent(context.Background(), core.Agent{ID: id, TenantID: "tenant-a", Name: id, HealthStatus: core.AgentHealthHealthy, Skills: []string{"research"}}); err != nil {
			t.Fatal(err)
		}
	}
	service := &hub.Service{Store: store, Adapter: completedAdapter{}}
	coordinator := &Coordinator{Service: service}
	result, err := coordinator.FanOut(context.Background(), "tenant-a", []FanoutInput{{AgentID: "agent-a", Text: "one"}, {Skill: "research", Text: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 2 || len(result.Failures) != 0 {
		t.Fatalf("fanout=%+v", result)
	}
	ids := []string{result.Tasks[0].ID, result.Tasks[1].ID}
	joined, err := coordinator.FanIn(context.Background(), "tenant-a", ids)
	if err != nil || len(joined.Tasks) != 2 {
		t.Fatalf("fanin=%+v err=%v", joined, err)
	}
}
