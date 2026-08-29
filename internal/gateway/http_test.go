package gateway

import (
	"context"
	"iter"
	"net"
	"net/http"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
)

type fakeAdapter struct{}

func (fakeAdapter) Discover(context.Context, string) (federation.Descriptor, error) {
	return federation.Descriptor{Name: "fake"}, nil
}
func (fakeAdapter) Send(context.Context, core.Agent, federation.Message) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateCompleted, Final: true}, nil)
	}
}
func (fakeAdapter) Get(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCompleted, Final: true}, nil
}
func (fakeAdapter) Cancel(context.Context, core.Agent, string) (federation.Observation, error) {
	return federation.Observation{State: core.TaskStateCanceled, Final: true}, nil
}
func (fakeAdapter) Subscribe(context.Context, core.Agent, string) iter.Seq2[federation.Observation, error] {
	return func(yield func(federation.Observation, error) bool) {
		yield(federation.Observation{State: core.TaskStateCompleted, Final: true}, nil)
	}
}

func TestGatewayHandlerAndAdapter(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: Handler{Adapter: fakeAdapter{}}}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	endpoint := "http://" + listener.Addr().String()
	adapter := &HTTPAdapter{Endpoint: endpoint, Client: &http.Client{}, Direct: fakeAdapter{}}
	observations := make([]federation.Observation, 0)
	for observation, err := range adapter.Send(context.Background(), core.Agent{ID: "agent"}, federation.Message{ID: "message", Text: "hello"}) {
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	if len(observations) != 1 || observations[0].State != core.TaskStateCompleted {
		t.Fatalf("observations=%+v", observations)
	}
}

func TestGatewayConstructorRequiresHTTPS(t *testing.T) {
	if _, err := NewHTTPAdapter("http://127.0.0.1:1", fakeAdapter{}, nil); err == nil {
		t.Fatal("HTTP gateway endpoint was accepted")
	}
}

var _ http.Handler = Handler{}
