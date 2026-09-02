package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type traceContextKey struct{}

type traceState struct {
	TraceID string
	SpanID  string
}

// HTTPTracer is a dependency-light OTLP/HTTP JSON exporter. It only exports
// span names, bounded safe attributes, status, and timing; provider prompts,
// artifacts, credentials, and response bodies are never accepted as span
// attributes by this package.
type HTTPTracer struct {
	Endpoint    string
	ServiceName string
	Client      *http.Client
	Headers     map[string]string
	AllowHTTP   bool
	MaxAttrs    int
}

type httpSpan struct {
	tracer   *HTTPTracer
	name     string
	traceID  string
	spanID   string
	parentID string
	started  time.Time
	attrs    map[string]string
	ended    bool
	mu       sync.Mutex
}

func NewHTTPTracer(endpoint, serviceName string) (*HTTPTracer, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("OTLP endpoint must be an HTTP(S) URL without user information")
	}
	return &HTTPTracer{Endpoint: strings.TrimRight(parsed.String(), "/"), ServiceName: strings.TrimSpace(serviceName), Client: &http.Client{Timeout: 5 * time.Second}, MaxAttrs: 32}, nil
}

func (t *HTTPTracer) Start(ctx context.Context, name string, attrs map[string]string) (context.Context, Span) {
	if t == nil || t.Endpoint == "" {
		return NoopTracer().Start(ctx, name, attrs)
	}
	parent, _ := ctx.Value(traceContextKey{}).(traceState)
	traceID := parent.TraceID
	if len(traceID) != 32 {
		traceID = randomHex(16)
	}
	spanID := randomHex(8)
	span := &httpSpan{tracer: t, name: sanitizeName(name), traceID: traceID, spanID: spanID, parentID: parent.SpanID, started: time.Now().UTC(), attrs: sanitizeAttrs(attrs, t.maxAttrs())}
	return context.WithValue(ctx, traceContextKey{}, traceState{TraceID: traceID, SpanID: spanID}), span
}

func (s *httpSpan) SetAttribute(key, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || len(s.attrs) >= s.tracer.maxAttrs() {
		return
	}
	if sanitized := sanitizeAttr(key, value); sanitized != "" {
		s.attrs[sanitizeName(key)] = sanitized
	}
}

func (s *httpSpan) End(spanErr error) {
	if s == nil || s.tracer == nil {
		return
	}
	if !s.tracer.endpointAllowed() {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	ended := time.Now().UTC()
	status := "OK"
	if spanErr != nil {
		status = "ERROR"
		s.attrs["error.type"] = sanitizeAttr("error.type", fmt.Sprintf("%T", spanErr))
	}
	span := otlpSpan{Name: s.name, TraceID: s.traceID, SpanID: s.spanID, ParentSpanID: s.parentID, StartTimeUnixNano: s.started.UnixNano(), EndTimeUnixNano: ended.UnixNano(), Attributes: makeAttributes(s.attrs), Status: otlpStatus{Code: status}}
	s.mu.Unlock()
	payload := otlpPayload{ResourceSpans: []otlpResourceSpan{{Resource: otlpResource{Attributes: []otlpAttribute{{Key: "service.name", Value: otlpValue{StringValue: s.tracer.ServiceName}}}}, ScopeSpans: []otlpScopeSpan{{Spans: []otlpSpan{span}}}}}}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, s.tracer.Endpoint+"/v1/traces", bytes.NewReader(encoded))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range s.tracer.Headers {
		if strings.TrimSpace(key) != "" && !strings.ContainsAny(key, "\r\n") && !strings.ContainsAny(value, "\r\n") {
			request.Header.Set(key, value)
		}
	}
	client := s.tracer.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
}

type otlpPayload struct {
	ResourceSpans []otlpResourceSpan `json:"resourceSpans"`
}
type otlpResourceSpan struct {
	Resource   otlpResource    `json:"resource"`
	ScopeSpans []otlpScopeSpan `json:"scopeSpans"`
}
type otlpScopeSpan struct {
	Spans []otlpSpan `json:"spans"`
}
type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}
type otlpSpan struct {
	Name              string          `json:"name"`
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	StartTimeUnixNano int64           `json:"startTimeUnixNano"`
	EndTimeUnixNano   int64           `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	Status            otlpStatus      `json:"status"`
}
type otlpStatus struct {
	Code string `json:"code"`
}
type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}
type otlpValue struct {
	StringValue string `json:"stringValue,omitempty"`
}

func (t *HTTPTracer) maxAttrs() int {
	if t != nil && t.MaxAttrs > 0 {
		return t.MaxAttrs
	}
	return 32
}

func (t *HTTPTracer) endpointAllowed() bool {
	if t == nil || strings.TrimSpace(t.Endpoint) == "" {
		return false
	}
	parsed, err := url.Parse(t.Endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return parsed.Scheme == "https" || (parsed.Scheme == "http" && t.AllowHTTP)
}
func makeAttributes(values map[string]string) []otlpAttribute {
	result := make([]otlpAttribute, 0, len(values))
	for key, value := range values {
		result = append(result, otlpAttribute{Key: key, Value: otlpValue{StringValue: value}})
	}
	return result
}
func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
func sanitizeAttr(key, value string) string {
	key = sanitizeName(key)
	if key == "" || sensitiveAttribute(key) {
		return ""
	}
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func sensitiveAttribute(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{"prompt", "secret", "token", "credential", "password", "authorization", "artifact", "body", "content", "input", "output"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
func sanitizeAttrs(values map[string]string, limit int) map[string]string {
	result := make(map[string]string)
	for key, value := range values {
		if len(result) >= limit {
			break
		}
		if sanitized := sanitizeAttr(key, value); sanitized != "" {
			result[sanitizeName(key)] = sanitized
		}
	}
	return result
}
func randomHex(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return strings.Repeat("0", size*2)
	}
	return hex.EncodeToString(value)
}
