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
