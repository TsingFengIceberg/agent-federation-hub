package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

func TestFileOutboxPublisherIsDurableAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events", "outbox.jsonl")
	item := core.OutboxItem{ID: "outbox-1", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-1", Topic: "task.status", Payload: json.RawMessage(`{"state":"WORKING"}`), CreatedAt: time.Now().UTC()}
	publisher, err := NewFileOutboxPublisher(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileOutboxPublisher(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Publish(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(contents)), "\n") + 1; lines != 1 {
		t.Fatalf("expected one durable line, got %d: %s", lines, contents)
	}
}

func TestHTTPOutboxPublisherSendsIdempotentEnvelope(t *testing.T) {
	var request *http.Request
	var payload []byte
	publisher, err := NewHTTPOutboxPublisher("https://collector.example/outbox", func(context.Context) (string, error) { return "secret", nil })
	if err != nil {
		t.Fatal(err)
	}
	publisher.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		request = r
		payload, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	item := core.OutboxItem{ID: "outbox-1", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-1", Topic: "task.status", Payload: json.RawMessage(`{"state":"WORKING"}`), CreatedAt: time.Now().UTC()}
	if err := publisher.Publish(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if request == nil {
		t.Fatal("publisher did not send a request")
	}
	if request.Header.Get("Idempotency-Key") != "tenant-a:event-1" || request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("X-AFH-Topic") != item.Topic {
		t.Fatalf("unexpected headers: %+v", request.Header)
	}
	var got core.OutboxItem
	if err := json.Unmarshal(payload, &got); err != nil || got.DedupKey != item.DedupKey {
		t.Fatalf("payload=%+v err=%v", got, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPOutboxPublisherRejectsNonHTTPSEndpoint(t *testing.T) {
	if _, err := NewHTTPOutboxPublisher("http://collector.example/outbox", nil); err == nil {
		t.Fatal("non-HTTPS outbox endpoint accepted")
	}
}
