package registry

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestHTTPRegistryClientUsesTenantScopedContract(t *testing.T) {
	var gotAgent core.Agent
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			response.WriteHeader(http.StatusOK)
		case "/v1/agents":
			if request.Method == http.MethodPost {
				if err := json.NewDecoder(request.Body).Decode(&gotAgent); err != nil {
					t.Fatal(err)
				}
				response.WriteHeader(http.StatusCreated)
				return
			}
			_ = json.NewEncoder(response).Encode([]core.Agent{{ID: "agent-a", TenantID: "tenant-a"}})
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	client := &HTTPClient{Endpoint: "http://" + listener.Addr().String(), Client: &http.Client{}}
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Register(context.Background(), core.Agent{ID: "agent-a", TenantID: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	if gotAgent.TenantID != "tenant-a" {
		t.Fatalf("registered agent=%+v", gotAgent)
	}
	agents, err := client.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].TenantID != "tenant-a" {
		t.Fatalf("agents=%+v", agents)
	}
}
