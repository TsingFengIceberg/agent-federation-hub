package a2afederation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

func TestSelectEndpointRequiresExactJSONRPCV1(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  *a2a.AgentInterface
		wantError bool
	}{
		{name: "exact", endpoint: &a2a.AgentInterface{URL: "https://agent.example", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: "1.0"}},
		{name: "old version", endpoint: &a2a.AgentInterface{URL: "https://agent.example", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: "0.3"}, wantError: true},
		{name: "other binding", endpoint: &a2a.AgentInterface{URL: "https://agent.example", ProtocolBinding: a2a.TransportProtocolGRPC, ProtocolVersion: "1.0"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := selectEndpoint(&a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{test.endpoint}})
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestSelectEndpointAcceptsPythonSDKJSONRPCSpelling(t *testing.T) {
	card := &a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{
		{URL: "https://agent.example", ProtocolBinding: a2a.TransportProtocol("JSON_RPC"), ProtocolVersion: "1.0"},
	}}
	endpoint, _, err := selectEndpointForProfiles(card, []BindingProfile{InitialBindingProfile})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.URL != "https://agent.example" {
		t.Fatalf("selected endpoint=%+v", endpoint)
	}
	normalizeCardBindings(card)
	if card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Fatalf("normalized binding=%q", card.SupportedInterfaces[0].ProtocolBinding)
	}
}

func TestConfiguredProfilesSelectHTTPJSONWithoutSDKFallback(t *testing.T) {
	card := &a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{
		{URL: "https://agent.example/rest", ProtocolBinding: a2a.TransportProtocolHTTPJSON, ProtocolVersion: a2a.Version},
		{URL: "https://agent.example/rpc", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version},
	}}
	endpoint, profile, err := selectEndpointForProfiles(card, []BindingProfile{{
		ProtocolVersion: string(a2a.Version), Binding: a2a.TransportProtocolHTTPJSON, StreamTransport: "SSE",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.URL != "https://agent.example/rest" || profile.Binding != a2a.TransportProtocolHTTPJSON {
		t.Fatalf("selected endpoint=%+v profile=%+v", endpoint, profile)
	}
}

func TestNewWithProfilesValidatesExplicitContract(t *testing.T) {
	if _, err := NewWithProfiles(time.Second, []BindingProfile{{
		ProtocolVersion: string(a2a.Version), Binding: a2a.TransportProtocolGRPC, StreamTransport: "SSE",
	}}); err == nil {
		t.Fatal("invalid gRPC stream transport was accepted")
	}
	if _, err := NewWithProfiles(time.Second, []BindingProfile{GRPCBindingProfile}); err != nil {
		t.Fatalf("valid gRPC profile rejected: %v", err)
	}
}

func TestParseBindingProfiles(t *testing.T) {
	profiles, err := ParseBindingProfiles("grpc, HTTP+JSON, json_rpc")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 || profiles[0].Binding != a2a.TransportProtocolGRPC ||
		profiles[1].Binding != a2a.TransportProtocolHTTPJSON || profiles[2].Binding != a2a.TransportProtocolJSONRPC {
		t.Fatalf("profiles=%+v", profiles)
	}
	if _, err := ParseBindingProfiles("smtp"); err == nil {
		t.Fatal("unsupported profile was accepted")
	}
}

func TestRequestedExtensionsAreValidatedDeduplicatedAndSorted(t *testing.T) {
	agent := core.Agent{Extensions: []string{"https://example.com/z", "https://example.com/a"}}
	ctx, extensions, err := attachExtensions(context.Background(), agent, []string{"https://example.com/z", "https://example.com/z", "https://example.com/a"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := fmt.Sprint(extensions); diff != "[https://example.com/a https://example.com/z]" {
		t.Fatalf("extensions=%s", diff)
	}
	_ = ctx
	if _, _, err := attachExtensions(context.Background(), agent, []string{"javascript:alert(1)"}); err == nil {
		t.Fatal("invalid extension URI accepted")
	}
	if _, _, err := attachExtensions(context.Background(), agent, []string{"https://example.com/unknown"}); err == nil {
		t.Fatal("undeclared extension accepted")
	}
}

type extensionTestHandler struct{ called bool }

func (h *extensionTestHandler) Validate(context.Context, core.Agent, map[string]any) error {
	h.called = true
	return nil
}

func TestExtensionPolicyCanRequireAndActivateHandlers(t *testing.T) {
	agent := core.Agent{Extensions: []string{"https://example.com/ext"}}
	if err := (ExtensionPolicy{RequireHandler: true}).Validate(context.Background(), agent, agent.Extensions, nil); err == nil {
		t.Fatal("strict extension policy accepted missing handler")
	}
	handler := &extensionTestHandler{}
	if err := (ExtensionPolicy{RequireHandler: true, Handlers: map[string]ExtensionHandler{"https://example.com/ext": handler}}).Validate(context.Background(), agent, agent.Extensions, map[string]any{"mode": "test"}); err != nil {
		t.Fatal(err)
	}
	if !handler.called {
		t.Fatal("extension handler was not called")
	}
}

func TestAgentCardJWSRoundTripAndTamperDetection(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	card := &a2a.AgentCard{Name: "signed", Description: "fixture", Version: "1", Skills: []a2a.AgentSkill{}}
	if err := SignAgentCard(card, key, "card-key-1"); err != nil {
		t.Fatal(err)
	}
	verifier := CardVerifier{Required: true, Resolver: StaticCardSignatureResolver{"card-key-1": &key.PublicKey}}
	if err := verifier.Verify(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	card.Name = "tampered"
	if err := verifier.Verify(context.Background(), card); err == nil {
		t.Fatal("tampered AgentCard was accepted")
	}
}

func TestValidateCredentialsRequiresDeclaredSingleSchemeCredential(t *testing.T) {
	card := &a2a.AgentCard{SecurityRequirements: a2a.SecurityRequirementsOptions{
		{a2a.SecuritySchemeName("oauth"): {}},
	}}
	store := a2aclient.NewInMemoryCredentialsStore()
	sessionID := a2aclient.SessionID("agent-1")
	err := validateCredentials(context.Background(), card, store, sessionID)
	var adapterErr *federation.Error
	if !errors.As(err, &adapterErr) || adapterErr.Problem.Category != "authentication" {
		t.Fatalf("missing credential error = %#v", err)
	}
	store.Set(sessionID, a2a.SecuritySchemeName("oauth"), "secret")
	if err := validateCredentials(context.Background(), card, store, sessionID); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCredentialsRejectsUnsupportedCompoundRequirement(t *testing.T) {
	card := &a2a.AgentCard{SecurityRequirements: a2a.SecurityRequirementsOptions{
		{
			a2a.SecuritySchemeName("oauth"): {},
			a2a.SecuritySchemeName("mtls"):  {},
		},
	}}
	store := a2aclient.NewInMemoryCredentialsStore()
	sessionID := a2aclient.SessionID("agent-1")
	store.Set(sessionID, a2a.SecuritySchemeName("oauth"), "one")
	store.Set(sessionID, a2a.SecuritySchemeName("mtls"), "two")
	if err := validateCredentials(context.Background(), card, store, sessionID); err == nil {
		t.Fatal("compound authentication requirement was accepted")
	}
}

func TestMapErrorCategoriesAndSanitizedMessages(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
	}{
		{name: "authentication", err: a2a.ErrUnauthenticated, category: "authentication"},
		{name: "authorization", err: a2a.ErrUnauthorized, category: "authorization"},
		{name: "validation", err: a2a.ErrInvalidParams, category: "validation"},
		{name: "resource", err: a2a.ErrTaskNotFound, category: "resource"},
		{name: "state", err: a2a.ErrTaskNotCancelable, category: "state"},
		{name: "protocol", err: a2a.ErrUnsupportedOperation, category: "protocol"},
		{name: "transport", err: errors.New("secret upstream detail"), category: "transport"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := mapError(test.err, true)
			var adapterErr *federation.Error
			if !errors.As(mapped, &adapterErr) {
				t.Fatalf("mapped type = %T", mapped)
			}
			if adapterErr.Problem.Category != test.category {
				t.Fatalf("category = %q", adapterErr.Problem.Category)
			}
			if adapterErr.Problem.Message == "secret upstream detail" {
				t.Fatal("upstream error leaked through sanitized problem")
			}
		})
	}
}

func TestDecodePushPreservesStructuredArrayAndStableDedupKey(t *testing.T) {
	payload := []byte(`{
		"artifactUpdate": {
			"taskId": "remote-1",
			"contextId": "context-1",
			"artifact": {"artifactId": "artifact-1", "parts": [{"data": [1, "two", true]}]},
			"lastChunk": true
		}
	}`)
	first, err := DecodePush(payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodePush(payload)
	if err != nil {
		t.Fatal(err)
	}
	if first.DedupKey != second.DedupKey || first.RemoteTaskID != "remote-1" {
		t.Fatalf("unstable observation: first=%+v second=%+v", first, second)
	}
	data, ok := first.Artifacts[0].Artifact.Parts[0].Data.([]any)
	if !ok || len(data) != 3 {
		t.Fatalf("structured data = %#v", first.Artifacts[0].Artifact.Parts[0].Data)
	}
}
