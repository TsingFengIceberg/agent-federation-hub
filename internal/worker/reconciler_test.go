package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type fakeTaskReconciler struct {
	mu    sync.Mutex
	calls int
	err   error
	wait  time.Duration
}

func (f *fakeTaskReconciler) ReconcileTask(ctx context.Context, _, _ string, _ bool) (core.Task, error) {
	if f.wait > 0 {
		timer := time.NewTimer(f.wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return core.Task{}, ctx.Err()
		case <-timer.C:
		}
	}
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return core.Task{}, f.err
}

func TestReconcilerLeasePreventsDuplicateWorkersAndSchedulesSuccess(t *testing.T) {
	store, task, now := recoverableJournalTask(t)
	defer store.Close()
	tasks := &fakeTaskReconciler{}
	workerA := &Reconciler{
		Store: store, Tasks: tasks, WorkerID: "worker-a", Now: func() time.Time { return now },
		LeaseDuration: time.Minute, PollInterval: 10 * time.Second,
	}
	count, err := workerA.RunOnce(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("worker A count=%d err=%v", count, err)
	}
	workerB := &Reconciler{
		Store: store, Tasks: tasks, WorkerID: "worker-b", Now: func() time.Time { return now.Add(time.Second) },
		LeaseDuration: time.Minute, PollInterval: 10 * time.Second,
	}
	count, err = workerB.RunOnce(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("worker B duplicated Task %s: count=%d err=%v", task.ID, count, err)
	}
	if tasks.calls != 1 {
		t.Fatalf("reconcile calls=%d", tasks.calls)
	}
}

func TestReconcilerFailureUsesBoundedBackoff(t *testing.T) {
	store, _, now := recoverableJournalTask(t)
	defer store.Close()
	tasks := &fakeTaskReconciler{err: errors.New("provider unavailable")}
	reconciler := &Reconciler{
		Store: store, Tasks: tasks, WorkerID: "worker-a", Now: func() time.Time { return now },
		LeaseDuration: time.Minute, BaseBackoff: 10 * time.Second, MaxBackoff: time.Minute,
	}
	count, err := reconciler.RunOnce(context.Background())
	if count != 1 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	early, err := store.ClaimRecoverable(context.Background(), "worker-b", 1, now.Add(9*time.Second), time.Minute)
	if err != nil || len(early) != 0 {
		t.Fatalf("backoff not enforced: leases=%+v err=%v", early, err)
	}
}

func TestReconcilerRenewsLeaseDuringSlowProviderCall(t *testing.T) {
	store, _, _ := recoverableJournalTask(t)
	defer store.Close()
	tasks := &fakeTaskReconciler{wait: 180 * time.Millisecond}
	reconciler := &Reconciler{
		Store: store, Tasks: tasks, WorkerID: "worker-a",
		LeaseDuration: 90 * time.Millisecond, PollInterval: time.Second,
	}
	done := make(chan error, 1)
	go func() {
		_, err := reconciler.RunOnce(context.Background())
		done <- err
	}()
	time.Sleep(120 * time.Millisecond)
	leases, err := store.ClaimRecoverable(context.Background(), "worker-b", 1, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("slow Task was leased twice: %+v", leases)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func recoverableJournalTask(t *testing.T) (*core.JournalStore, core.Task, time.Time) {
	t.Helper()
	store, err := core.OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := store.CreateTask(context.Background(), core.Task{
		ID: core.NewID(), TenantID: "tenant-a", AgentID: "agent-1", RemoteTaskID: "remote-1",
		State: core.TaskStateWorking, CreatedAt: now, UpdatedAt: now,
	}, core.Event{Type: "task.status", State: core.TaskStateWorking, CreatedAt: now})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, task, now
}
