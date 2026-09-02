package observability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPTracerExportsBoundedSafeSpan(t *testing.T) {
	var payload map[string]any
	tracer, err := NewHTTPTracer("https://collector.example", "hub-test")
	if err != nil {
		t.Fatal(err)
	}
	tracer.Headers = map[string]string{"Authorization": "Bearer collector-token"}
	tracer.Client = &http.Client{Transport: roundTripTraceFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/traces" || request.Header.Get("Authorization") != "Bearer collector-token" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	ctx, span := tracer.Start(context.Background(), strings.Repeat("x", 200), map[string]string{"tenant.id": "tenant-a", "prompt": strings.Repeat("secret", 200)})
	span.SetAttribute("task.id", "task-1")
	span.End(nil)
	if len(payload) == 0 {
		t.Fatal("span was not exported")
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("sensitive span attribute leaked")
	}
	if !strings.Contains(string(encoded), "hub-test") || !strings.Contains(string(encoded), "task-1") {
		t.Fatalf("span payload missing expected fields: %s", encoded)
	}
	if _, ok := ctx.Value(traceContextKey{}).(traceState); !ok {
		t.Fatal("trace context was not propagated")
	}
}

func TestHTTPTracerRejectsUserInfoEndpoint(t *testing.T) {
	if _, err := NewHTTPTracer("https://user:pass@collector.example", "hub"); err == nil {
		t.Fatal("userinfo in OTLP endpoint accepted")
	}
}

func TestHTTPTracerRequiresExplicitInsecureOptIn(t *testing.T) {
	tracer, err := NewHTTPTracer("http://collector.example", "hub")
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	tracer.Client = &http.Client{Transport: roundTripTraceFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	_, span := tracer.Start(context.Background(), "blocked", nil)
	span.End(nil)
	if requests != 0 {
		t.Fatalf("insecure endpoint exported without opt-in: %d requests", requests)
	}

	tracer.AllowHTTP = true
	_, span = tracer.Start(context.Background(), "allowed", nil)
	span.End(nil)
	if requests != 1 {
		t.Fatalf("insecure endpoint requests=%d, want 1 after opt-in", requests)
	}
}

type roundTripTraceFunc func(*http.Request) (*http.Response, error)

func (f roundTripTraceFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
