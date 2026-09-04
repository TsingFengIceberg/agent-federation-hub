package a2afederation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/federation"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/netpolicy"
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

func TestProtocolVersionsCompareMajorMinorAndIgnorePatch(t *testing.T) {
	for _, test := range []struct {
		advertised string
		required   string
		want       bool
	}{
		{advertised: "1.0", required: "1.0", want: true},
		{advertised: "1.0.1", required: "1.0", want: true},
		{advertised: "1.0.99", required: "1.0.2", want: true},
		{advertised: "1.1", required: "1.0", want: false},
		{advertised: "2.0.0", required: "1.0", want: false},
		{advertised: "1", required: "1.0", want: false},
		{advertised: "1.0.beta", required: "1.0", want: false},
		{advertised: "01.0", required: "1.0", want: false},
	} {
		t.Run(test.advertised+"-"+test.required, func(t *testing.T) {
			if got := ProtocolVersionsCompatible(test.advertised, test.required); got != test.want {
				t.Fatalf("ProtocolVersionsCompatible(%q, %q)=%v, want %v", test.advertised, test.required, got, test.want)
			}
		})
	}
}

func TestCanonicalProtocolVersionUsesMajorMinorOnWire(t *testing.T) {
	for _, test := range []struct {
		input, want string
	}{
		{input: "1.0", want: "1.0"},
		{input: "1.0.7", want: "1.0"},
		{input: "2.3.99", want: "2.3"},
	} {
		got, err := CanonicalProtocolVersion(test.input)
		if err != nil || got != test.want {
			t.Fatalf("CanonicalProtocolVersion(%q)=%q err=%v, want %q", test.input, got, err, test.want)
		}
	}
	if _, err := CanonicalProtocolVersion("1"); err == nil {
		t.Fatal("malformed protocol version was canonicalized")
	}
}

func TestBindingProfileRejectsMalformedProtocolVersion(t *testing.T) {
	profile := InitialBindingProfile
	profile.ProtocolVersion = "1"
	if err := profile.Validate(); err == nil {
		t.Fatal("malformed protocol version was accepted")
	}
}

func TestSelectEndpointAcceptsPatchVersion(t *testing.T) {
	card := &a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{{
		URL: "https://agent.example", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: "1.0.3",
	}}}
	endpoint, _, err := selectEndpointForProfiles(card, []BindingProfile{InitialBindingProfile})
	if err != nil || endpoint.URL != "https://agent.example" {
		t.Fatalf("patch-compatible endpoint=%+v err=%v", endpoint, err)
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

func TestSetHTTPClientWithPolicyCannotBypassUnsafeScheme(t *testing.T) {
	adapter := New(time.Second)
	called := false
	adapter.SetHTTPClientWithPolicy(&http.Client{Transport: a2aRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}, netpolicy.HTTPSOnlyPolicy())
	request, err := http.NewRequest(http.MethodGet, "http://public.example/.well-known/agent-card.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.transportClient.Do(request); err == nil {
		t.Fatal("custom A2A transport bypassed HTTPS policy")
	}
	if called {
		t.Fatal("unsafe A2A request reached custom transport")
	}
}

type a2aRoundTripFunc func(*http.Request) (*http.Response, error)

func (f a2aRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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
	profiles, err = ParseBindingProfiles("a2a-v1-http-json-sse")
	if err != nil || len(profiles) != 1 || ProfileName(profiles[0]) != "a2a-v1-http-json-sse" {
		t.Fatalf("named profile=%+v err=%v", profiles, err)
	}
	if _, err := ParseBindingProfiles("JSONRPC,a2a-v1-jsonrpc-sse"); err == nil {
		t.Fatal("duplicate named profile was accepted")
	}
}

func TestKnownBindingProfilesHaveStableNames(t *testing.T) {
	profiles := KnownBindingProfiles()
	for _, name := range []string{"a2a-v1-jsonrpc-sse", "a2a-v1-http-json-sse", "a2a-v1-grpc"} {
		if _, ok := profiles[name]; !ok {
			t.Fatalf("known profile %q is missing: %+v", name, profiles)
		}
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

func TestAdapterRevalidatesAgentCardSignatureAfterAdmission(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	card := &a2a.AgentCard{
		Name: "signed", Description: "fixture", Version: "1",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: "https://agent.example/a2a", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
		}},
	}
	if err := SignAgentCard(card, key, "card-key-1"); err != nil {
		t.Fatal(err)
	}
	adapter := New(time.Second)
	adapter.SetCardVerifier(CardVerifier{Required: true, Resolver: StaticCardSignatureResolver{"card-key-1": &key.PublicKey}})
	if err := adapter.verifyCard(t.Context(), card); err != nil {
		t.Fatalf("signed Card admission failed: %v", err)
	}

	// The same verifier is used by the later outbound client path. A changed
	// Card must be rejected even though the admission-time Card was valid.
	card.Name = "tampered after admission"
	err = adapter.verifyCard(t.Context(), card)
	var adapterErr *federation.Error
	if !errors.As(err, &adapterErr) || adapterErr.Problem.Code != "AGENT_CARD_SIGNATURE_INVALID" {
		t.Fatalf("tampered Card verification result=%v", err)
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

func TestRetryContinuationDoesNotReplayNewTask(t *testing.T) {
	calls := 0
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("new"))}
	_, err := retryContinuation(context.Background(), request, func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 5, 0, 0)
	if calls != 1 {
		t.Fatalf("new Task sender called %d times, want 1", calls)
	}
	if err == nil || !isExecutionInProgressError(err) {
		t.Fatalf("error=%v, want original admission error", err)
	}
}

func TestRetryContinuationRetriesExactAdmissionErrorForExistingTask(t *testing.T) {
	calls := 0
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))}
	request.Message.TaskID = "remote-task"
	wanted := &a2a.Task{}
	result, err := retryContinuation(context.Background(), request, func() (a2a.SendMessageResult, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("task execution is already in progress")
		}
		return wanted, nil
	}, 5, 0, 0)
	if err != nil || result != wanted {
		t.Fatalf("result=%v err=%v, want accepted", result, err)
	}
	if calls != 3 {
		t.Fatalf("existing Task sender called %d times, want 3", calls)
	}
}

func TestRetryContinuationStopsAtBoundedAttemptCount(t *testing.T) {
	calls := 0
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))}
	request.Message.TaskID = "remote-task"
	_, err := retryContinuation(context.Background(), request, func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 3, 0, 0)
	if calls != 3 {
		t.Fatalf("sender called %d times, want bounded 3", calls)
	}
	if err == nil || !isExecutionInProgressError(err) {
		t.Fatalf("error=%v, want final admission error", err)
	}
}

func TestRetryContinuationDoesNotRetryUnrelatedTransportError(t *testing.T) {
	calls := 0
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))}
	request.Message.TaskID = "remote-task"
	_, err := retryContinuation(context.Background(), request, func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("upstream connection reset")
	}, 5, 0, 0)
	if calls != 1 {
		t.Fatalf("unrelated error caused %d attempts, want 1", calls)
	}
	if err == nil || err.Error() != "upstream connection reset" {
		t.Fatalf("error=%v, want original transport error", err)
	}
}

func TestRetryContinuationHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	request := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))}
	request.Message.TaskID = "remote-task"
	_, err := retryContinuation(ctx, request, func() (a2a.SendMessageResult, error) {
		calls++
		return nil, errors.New("task execution is already in progress")
	}, 5, 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("canceled sender called %d times, want 0", calls)
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

func TestA2APartsFromCorePreservesTypedInputAndRejectsUnresolvedObject(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("report bytes"))
	parts, err := a2aPartsFromCore([]core.Part{
		{Kind: core.PartText, Text: "review", MediaType: "text/plain"},
		{Kind: core.PartData, Data: map[string]any{"priority": "high"}, MediaType: "application/json"},
		{Kind: core.PartFile, BytesBase64: encoded, MediaType: "application/octet-stream", Filename: "report.bin"},
		{Kind: core.PartFile, URI: "https://files.example/report.pdf", MediaType: "application/pdf", Filename: "report.pdf"},
	})
	if err != nil || len(parts) != 4 {
		t.Fatalf("parts=%+v err=%v", parts, err)
	}
	if value, ok := parts[0].Content.(a2a.Text); !ok || string(value) != "review" {
		t.Fatalf("text part=%+v", parts[0])
	}
	data, dataOK := parts[1].Content.(a2a.Data)
	dataObject, objectOK := data.Value.(map[string]any)
	if !dataOK || !objectOK || dataObject["priority"] != "high" {
		t.Fatalf("data part=%+v", parts[1])
	}
	if value, ok := parts[2].Content.(a2a.Raw); !ok || string(value) != "report bytes" || parts[2].Filename != "report.bin" {
		t.Fatalf("raw part=%+v", parts[2])
	}
	if value, ok := parts[3].Content.(a2a.URL); !ok || string(value) != "https://files.example/report.pdf" || parts[3].MediaType != "application/pdf" {
		t.Fatalf("URL part=%+v", parts[3])
	}
	if _, err := a2aPartsFromCore([]core.Part{{Kind: core.PartFile, ObjectID: "tenant-owned-object"}}); err == nil {
		t.Fatal("unresolved Artifact object reference reached the A2A adapter")
	}
}
