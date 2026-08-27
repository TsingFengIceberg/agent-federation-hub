package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalPermissionsAreRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal permissions=%o", info.Mode().Perm())
	}
}

func TestJournalReplayAndDuplicateSuppression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	ctx := context.Background()
	store, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, Task{
		ID: "local-1", TenantID: "tenant-a", AgentID: "agent-1",
		State: TaskStateSubmitted, Delivery: DeliveryPending,
		CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.submitted", Source: "hub", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	apply := func(task *Task) (Event, error) {
		task.State = TaskStateWorking
		task.RemoteTaskID = "remote-1"
		task.UpdatedAt = now.Add(time.Second)
		return Event{Type: "task.status", Source: "a2a", State: task.State, CreatedAt: task.UpdatedAt}, nil
	}
	task, applied, err := store.ApplyTask(ctx, "tenant-a", task.ID, "remote:event-1", apply)
	if err != nil || !applied {
		t.Fatalf("first apply: applied=%v err=%v", applied, err)
	}
	duplicate, applied, err := store.ApplyTask(ctx, "tenant-a", task.ID, "remote:event-1", apply)
	if err != nil || applied {
		t.Fatalf("duplicate apply: applied=%v err=%v", applied, err)
	}
	if duplicate.Revision != task.Revision {
		t.Fatalf("duplicate changed revision: got %d want %d", duplicate.Revision, task.Revision)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetTask(ctx, "tenant-a", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != TaskStateWorking || recovered.RemoteTaskID != "remote-1" {
		t.Fatalf("unexpected recovered task: %+v", recovered)
	}
	events, err := reopened.EventsAfter(ctx, "tenant-a", task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestTenantIsolationMasksTaskExistence(t *testing.T) {
	ctx := context.Background()
	store, err := OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	_, err = store.CreateTask(ctx, Task{
		ID: "local-1", TenantID: "tenant-a", AgentID: "agent-1",
		State: TaskStateSubmitted, Delivery: DeliveryPending,
		CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.submitted", Source: "hub", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTask(ctx, "tenant-b", "local-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant lookup returned %v", err)
	}
}

func TestJournalOptimisticRevisionAndMutationRollback(t *testing.T) {
	store, err := OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	task, err := store.CreateTask(context.Background(), Task{
		ID: "task-cas", TenantID: "tenant-a", AgentID: "agent-1",
		State: TaskStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.submitted", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_, applied, err := store.ApplyTaskVersion(context.Background(), "tenant-a", task.ID, task.Revision+1, "cas", func(task *Task) (Event, error) {
		task.State = TaskStateWorking
		return Event{}, nil
	})
	if !errors.Is(err, ErrRevisionConflict) || applied {
		t.Fatalf("stale revision applied=%v err=%v", applied, err)
	}
	mutationError := errors.New("mutation failed")
	_, applied, err = store.ApplyTaskVersion(context.Background(), "tenant-a", task.ID, task.Revision, "rollback", func(task *Task) (Event, error) {
		task.State = TaskStateFailed
		return Event{}, mutationError
	})
	if !errors.Is(err, mutationError) || applied {
		t.Fatalf("failed mutation applied=%v err=%v", applied, err)
	}
	current, err := store.GetTask(context.Background(), "tenant-a", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != task.Revision || current.State != TaskStateSubmitted {
		t.Fatalf("failed mutation changed task: %+v", current)
	}
}

func TestJournalWorkLeaseExclusionExpiryAndRetrySchedule(t *testing.T) {
	store, err := OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	task, err := store.CreateTask(ctx, Task{
		ID: "task-lease", TenantID: "tenant-a", AgentID: "agent-1", RemoteTaskID: "remote-1",
		State: TaskStateWorking, CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.status", State: TaskStateWorking, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimRecoverable(ctx, "worker-a", 1, now, time.Minute)
	if err != nil || len(first) != 1 || first[0].Task.ID != task.ID || first[0].Attempt != 1 {
		t.Fatalf("first leases=%+v err=%v", first, err)
	}
	second, err := store.ClaimRecoverable(ctx, "worker-b", 1, now.Add(30*time.Second), time.Minute)
	if err != nil || len(second) != 0 {
		t.Fatalf("concurrent lease=%+v err=%v", second, err)
	}
	takenOver, err := store.ClaimRecoverable(ctx, "worker-b", 1, now.Add(61*time.Second), time.Minute)
	if err != nil || len(takenOver) != 1 || takenOver[0].Attempt != 2 {
		t.Fatalf("expired lease takeover=%+v err=%v", takenOver, err)
	}
	if err := store.ReleaseLease(ctx, takenOver[0], now.Add(5*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	early, err := store.ClaimRecoverable(ctx, "worker-c", 1, now.Add(4*time.Minute), time.Minute)
	if err != nil || len(early) != 0 {
		t.Fatalf("retry schedule ignored: leases=%+v err=%v", early, err)
	}
	retried, err := store.ClaimRecoverable(ctx, "worker-c", 1, now.Add(5*time.Minute), time.Minute)
	if err != nil || len(retried) != 1 || retried[0].Attempt != 3 {
		t.Fatalf("scheduled retry=%+v err=%v", retried, err)
	}
}
