package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileAuditSinkPersistsAndRestrictsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit", "events.jsonl")
	sink, err := OpenFileAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	record := AuditRecord{
		Timestamp: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
		RequestID: "request-1", Decision: "authorization_denied", Action: ActionTaskRead,
		Subject: "subject-1", TenantID: "tenant-a", Reason: "insufficient_scope",
	}
	if err := sink.Record(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audit mode=%o", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuditRecord
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RequestID != record.RequestID || decoded.TenantID != record.TenantID {
		t.Fatalf("decoded=%+v", decoded)
	}
	if decoded.Version != 1 || decoded.Sequence != 1 || decoded.IntegrityHash == "" {
		t.Fatalf("audit chain metadata=%+v", decoded)
	}
}

func TestFileAuditSinkRejectsTamperedChainOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := OpenFileAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Record(context.Background(), AuditRecord{RequestID: "chain-1", Decision: "allowed"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.Replace(content, []byte("chain-1"), []byte("tampered"), 1)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileAuditSink(path); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("tampered chain error=%v", err)
	}
}

func TestHTTPAuditSinkUsesHTTPSAndBearerCallback(t *testing.T) {
	var body bytes.Buffer
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer collector-token" {
			t.Fatalf("authorization=%q", got)
		}
		_, _ = body.ReadFrom(r.Body)
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})}
	sink, err := NewHTTPAuditSink("https://collector.example/audit", func(context.Context) (string, error) {
		return "collector-token", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sink.Client = client
	if err := sink.Record(context.Background(), AuditRecord{RequestID: "request-1", Decision: "allowed"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "request-1") {
		t.Fatalf("collector body=%s", body.String())
	}
}

func TestHTTPAuditSinkRejectsNonHTTPSAndCollectorFailure(t *testing.T) {
	if _, err := NewHTTPAuditSink("http://collector.example/audit", nil); err == nil {
		t.Fatal("non-HTTPS audit endpoint accepted")
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("down")), Header: make(http.Header), Request: r}, nil
	})}
	sink, err := NewHTTPAuditSink("https://collector.example/audit", nil)
	if err != nil {
		t.Fatal(err)
	}
	sink.Client = client
	if err := sink.Record(context.Background(), AuditRecord{RequestID: "request-2"}); err == nil {
		t.Fatal("collector outage was accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestFanoutAuditSinkRetainsLocalRecordWhenCollectorFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	local, err := OpenFileAuditSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	collectorErr := errors.New("collector unavailable")
	fanout := FanoutAuditSink{local, AuditSinkFunc(func(context.Context, AuditRecord) error { return collectorErr })}
	if err := fanout.Record(context.Background(), AuditRecord{RequestID: "request-3"}); !errors.Is(err, collectorErr) {
		t.Fatalf("fanout error=%v", err)
	}
	if content, err := os.ReadFile(path); err != nil || !strings.Contains(string(content), "request-3") {
		t.Fatalf("local record content=%s err=%v", content, err)
	}
}

func TestRetryingAuditSinkRetriesBoundedFailures(t *testing.T) {
	var attempts int
	sink := AuditSinkFunc(func(context.Context, AuditRecord) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary collector failure")
		}
		return nil
	})
	retrying := &RetryingAuditSink{
		Sink: sink, Attempts: 3,
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	if err := retrying.Record(context.Background(), AuditRecord{RequestID: "retry-1"}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d, want 3", attempts)
	}
}

func TestRetryingAuditSinkStopsAfterBound(t *testing.T) {
	var attempts int
	retrying := &RetryingAuditSink{
		Sink: AuditSinkFunc(func(context.Context, AuditRecord) error {
			attempts++
			return errors.New("collector unavailable")
		}),
		Attempts: 2,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
	if err := retrying.Record(context.Background(), AuditRecord{RequestID: "retry-2"}); err == nil {
		t.Fatal("bounded retry unexpectedly succeeded")
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
}
