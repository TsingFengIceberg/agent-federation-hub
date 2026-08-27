package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type fakeOutboxPublisher struct {
	calls int
	err   error
}

func (p *fakeOutboxPublisher) Publish(_ context.Context, _ core.OutboxItem) error {
	p.calls++
	return p.err
}

func TestOutboxProcessorAcknowledgesOnlyAfterPublish(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	item := core.OutboxItem{ID: "outbox-1", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-1", Topic: "task.status", Payload: json.RawMessage(`{"state":"WORKING"}`), CreatedAt: now}
	if created, err := store.EnqueueOutbox(context.Background(), item); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	publisher := &fakeOutboxPublisher{}
	processor := &OutboxProcessor{Store: store, Publisher: publisher, WorkerID: "publisher-a", Now: func() time.Time { return now }}
	if count, err := processor.RunOnce(context.Background()); err != nil || count != 1 || publisher.calls != 1 {
		t.Fatalf("count=%d calls=%d err=%v", count, publisher.calls, err)
	}
	if count, err := processor.RunOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("acked item redelivered: count=%d err=%v", count, err)
	}
}

func TestOutboxProcessorRetriesFailedPublish(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := store.EnqueueOutbox(context.Background(), core.OutboxItem{
		ID: "outbox-1", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-1", Topic: "task.status", Payload: json.RawMessage(`{}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	processor := &OutboxProcessor{
		Store: store, Publisher: &fakeOutboxPublisher{err: errors.New("collector unavailable")}, WorkerID: "publisher-a",
		Now: func() time.Time { return now }, BaseBackoff: 10 * time.Second,
	}
	if count, err := processor.RunOnce(context.Background()); count != 1 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if leases, err := store.ClaimOutbox(context.Background(), "publisher-b", 1, now.Add(9*time.Second), time.Minute); err != nil || len(leases) != 0 {
		t.Fatalf("retry delay ignored: leases=%+v err=%v", leases, err)
	}
}

func TestOutboxProcessorDeadLettersAfterMaximumAttempts(t *testing.T) {
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := store.EnqueueOutbox(context.Background(), core.OutboxItem{
		ID: "outbox-dlq", TenantID: "tenant-a", TaskID: "task-1", DedupKey: "event-dlq", Topic: "task.status", Payload: json.RawMessage(`{}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	processor := &OutboxProcessor{
		Store: store, Publisher: &fakeOutboxPublisher{err: errors.New("collector permanently unavailable")}, WorkerID: "publisher-a",
		Now: func() time.Time { return now }, MaxAttempts: 1,
	}
	if count, err := processor.RunOnce(context.Background()); count != 1 || err == nil {
		t.Fatalf("count=%d err=%v, want one dead-lettered item and an observable error", count, err)
	}
	if leases, err := store.ClaimOutbox(context.Background(), "publisher-b", 1, now.Add(time.Hour), time.Minute); err != nil || len(leases) != 0 {
		t.Fatalf("dead-lettered item was claimable: leases=%+v err=%v", leases, err)
	}
}
