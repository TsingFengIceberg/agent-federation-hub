package a2afederation

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/interop"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
	"github.com/a2aproject/a2a-go/v2/a2a"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestAdapterUsesExplicitGRPCProfile(t *testing.T) {
	const bufferSize = 1 << 20
	listener := bufconn.Listen(bufferSize)
	grpcServer := grpc.NewServer()
	handler := a2asrv.NewHandler(interop.ScenarioExecutor{})
	grpcHandler := a2agrpc.NewHandler(handler)
	a2apb.RegisterA2AServiceServer(grpcServer, grpcHandler)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	card := &a2a.AgentCard{
		Name: "gRPC fixture", Version: "0.1.0",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: "passthrough:///bufnet", ProtocolBinding: a2a.TransportProtocolGRPC,
			ProtocolVersion: a2a.Version,
		}},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []a2a.AgentSkill{{ID: "interop"}},
	}
	cardListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cardServer := &http.Server{Handler: a2asrv.NewStaticAgentCardHandler(card)}
	go func() { _ = cardServer.Serve(cardListener) }()
	t.Cleanup(func() {
		_ = cardServer.Close()
	})

	adapter, err := NewWithProfilesAndGRPCOptions(time.Second, []BindingProfile{GRPCBindingProfile}, []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	})
	if err != nil {
		t.Fatal(err)
	}
	cardURL := "http://" + cardListener.Addr().String() + "/.well-known/agent-card.json"
	descriptor, err := adapter.Discover(context.Background(), cardURL)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ProtocolBinding != string(a2a.TransportProtocolGRPC) {
		t.Fatalf("binding=%q", descriptor.ProtocolBinding)
	}

	observations := make([]federation.Observation, 0)
	for observation, sendErr := range adapter.Send(context.Background(), core.Agent{
		ID: "grpc-fixture", CardURL: cardURL,
		ProtocolBinding: string(a2a.TransportProtocolGRPC), ProtocolVersion: string(a2a.Version),
		Endpoint: "passthrough:///bufnet", Streaming: true,
	}, federation.Message{ID: "grpc-message", Text: "message", ReturnImmediately: true}) {
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		observations = append(observations, observation)
	}
	if len(observations) != 1 || observations[0].State != core.TaskStateCompleted {
		t.Fatalf("observations=%+v", observations)
	}
}

func TestAdapterGRPCPropagatesDeclaredBearerCredential(t *testing.T) {
	const bufferSize = 1 << 20
	listener := bufconn.Listen(bufferSize)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		values := metadata.ValueFromIncomingContext(ctx, "authorization")
		if len(values) != 1 || values[0] != "Bearer grpc-secret" {
			return nil, status.Error(codes.Unauthenticated, "missing gRPC bearer credential")
		}
		return handler(ctx, req)
	}))
	grpcHandler := a2agrpc.NewHandler(a2asrv.NewHandler(interop.ScenarioExecutor{}))
	a2apb.RegisterA2AServiceServer(grpcServer, grpcHandler)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	card := &a2a.AgentCard{
		Name: "authenticated gRPC fixture", Version: "0.1.0",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: "passthrough:///bufnet-auth", ProtocolBinding: a2a.TransportProtocolGRPC,
			ProtocolVersion: a2a.Version,
		}},
		Capabilities:    a2a.AgentCapabilities{Streaming: true},
		SecuritySchemes: a2a.NamedSecuritySchemes{"oauth": a2a.HTTPAuthSecurityScheme{}},
		SecurityRequirements: a2a.SecurityRequirementsOptions{{
			a2a.SecuritySchemeName("oauth"): {},
		}},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []a2a.AgentSkill{{ID: "interop"}},
	}
	cardServer := httptest.NewServer(a2asrv.NewStaticAgentCardHandler(card))
	t.Cleanup(cardServer.Close)

	adapter, err := NewWithProfilesAndGRPCOptions(time.Second, []BindingProfile{GRPCBindingProfile}, []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, secrets.NewEnvProviderForTest(map[string]string{"A2A_GRPC_TOKEN": "grpc-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	cardURL := cardServer.URL + "/.well-known/agent-card.json"
	if _, err := adapter.Discover(context.Background(), cardURL); err != nil {
		t.Fatal(err)
	}
	var observations []federation.Observation
	for observation, sendErr := range adapter.Send(context.Background(), core.Agent{
		ID: "grpc-auth", CardURL: cardURL, ProtocolBinding: string(a2a.TransportProtocolGRPC),
		ProtocolVersion: string(a2a.Version), Endpoint: "passthrough:///bufnet-auth",
		Streaming: true, SecuritySchemes: []string{"oauth"},
		CredentialEnv: map[string]string{"oauth": "A2A_GRPC_TOKEN"},
	}, federation.Message{ID: "grpc-auth-message", Text: "message", ReturnImmediately: true}) {
		if sendErr != nil {
			t.Fatal(sendErr)
		}
		observations = append(observations, observation)
	}
	if len(observations) != 1 || observations[0].State != core.TaskStateCompleted {
		t.Fatalf("observations=%+v", observations)
	}
}
