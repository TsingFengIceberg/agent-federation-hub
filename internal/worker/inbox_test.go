package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type fakeInboxApplier struct {
	calls int
	err   error
}

func (f *fakeInboxApplier) ApplyInboxItem(_ context.Context, _ core.InboxItem) (core.Task, error) {
	f.calls++
	return core.Task{}, f.err
}

func TestInboxProcessorAcknowledgesOnlyAfterApply(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]string{"state": "COMPLETED"})
	item := core.InboxItem{
		ID: "inbox-1", TenantID: "tenant-a", TaskID: "task-1",
		DedupKey: "push-1", Protocol: "a2a", Payload: payload, CreatedAt: now,
	}
	created, err := store.EnqueueInbox(context.Background(), item)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	duplicate, err := store.EnqueueInbox(context.Background(), core.InboxItem{
		ID: "inbox-2", TenantID: item.TenantID, TaskID: item.TaskID,
		DedupKey: item.DedupKey, Protocol: item.Protocol, Payload: payload, CreatedAt: now,
	})
	if err != nil || duplicate {
		t.Fatalf("duplicate created=%v err=%v", duplicate, err)
	}
	applier := &fakeInboxApplier{}
	processor := &InboxProcessor{
		Store: store, Apply: applier, WorkerID: "worker-a", Now: func() time.Time { return now },
	}
	count, err := processor.RunOnce(context.Background())
	if err != nil || count != 1 || applier.calls != 1 {
		t.Fatalf("count=%d calls=%d err=%v", count, applier.calls, err)
	}
	count, err = processor.RunOnce(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("acked item redelivered: count=%d err=%v", count, err)
	}
}

func TestInboxProcessorRetriesFailedApply(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	_, err = store.EnqueueInbox(context.Background(), core.InboxItem{
		ID: "inbox-1", TenantID: "tenant-a", TaskID: "task-1",
		DedupKey: "push-1", Protocol: "a2a", Payload: json.RawMessage(`{}`), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	applier := &fakeInboxApplier{err: errors.New("temporary failure")}
	processor := &InboxProcessor{
		Store: store, Apply: applier, WorkerID: "worker-a", Now: func() time.Time { return now },
		BaseBackoff: 10 * time.Second,
	}
	if count, err := processor.RunOnce(context.Background()); count != 1 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if leases, err := store.ClaimInbox(context.Background(), "worker-b", 1, now.Add(9*time.Second), time.Minute); err != nil || len(leases) != 0 {
		t.Fatalf("retry delay ignored: leases=%+v err=%v", leases, err)
	}
}
