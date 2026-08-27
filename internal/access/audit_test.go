package access

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
}
