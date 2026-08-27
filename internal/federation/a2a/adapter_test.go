package a2afederation

import (
	"context"
	"errors"
	"testing"

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
