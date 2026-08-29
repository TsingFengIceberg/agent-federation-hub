package core

import (
	"context"
	"encoding/json"
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

func TestJournalHealthHonorsContextAndFileState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	store, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("healthy journal: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Health(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled journal health=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
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

func TestJournalBackupAndRestoreAreReplayable(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.journal")
	backupPath := filepath.Join(directory, "backup", "hub.journal")
	manifestPath := filepath.Join(directory, "backup", "hub.journal.manifest.json")
	restoredPath := filepath.Join(directory, "restored", "hub.journal")
	store, err := OpenJournal(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateTask(ctx, Task{
		ID: "backup-task", TenantID: "tenant-a", AgentID: "agent-1",
		State: TaskStateWorking, Delivery: DeliveryPending, CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.status", State: TaskStateWorking, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BackupWithManifest(backupPath, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreJournalBackupWithManifest(backupPath, manifestPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenJournal(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	task, err := restored.GetTask(ctx, "tenant-a", "backup-task")
	if err != nil || task.State != TaskStateWorking {
		t.Fatalf("restored task=%+v err=%v", task, err)
	}
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions info=%v err=%v", info, err)
	}
	if info, err := os.Stat(manifestPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest permissions info=%v err=%v", info, err)
	}
	if err := os.WriteFile(backupPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreJournalBackupWithManifest(backupPath, manifestPath, filepath.Join(directory, "tampered.journal")); err == nil {
		t.Fatal("tampered journal backup passed manifest verification")
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

func TestJournalTokenRevocationExpiry(t *testing.T) {
	store, err := OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	revocation := TokenRevocation{
		Issuer: "https://issuer.example", TokenID: "token-1", TenantID: "tenant-a",
		RevokedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.RevokeToken(context.Background(), revocation); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.TokenRevoked(context.Background(), revocation.Issuer, revocation.TokenID, revocation.TenantID, now)
	if err != nil || !revoked {
		t.Fatalf("revoked=%v err=%v", revoked, err)
	}
	removed, err := store.PruneRevocations(context.Background(), now.Add(time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	revoked, err = store.TokenRevoked(context.Background(), revocation.Issuer, revocation.TokenID, revocation.TenantID, now.Add(time.Hour))
	if err != nil || revoked {
		t.Fatalf("expired revocation active=%v err=%v", revoked, err)
	}
}

func TestJournalArtifactQuotaReplayAndDeletionLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	store, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	object := ArtifactObject{
		ID: "object-1", TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "artifact-a",
		SHA256: "digest", SizeBytes: 10, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	reserved, created, err := store.ReserveArtifact(context.Background(), object, ArtifactQuota{MaxBytes: 10, MaxObjects: 1})
	if err != nil || !created || reserved.Status != ArtifactObjectPending {
		t.Fatalf("reserve=%+v created=%v err=%v", reserved, created, err)
	}
	if _, _, err := store.ReserveArtifact(context.Background(), ArtifactObject{
		ID: "object-2", TenantID: "tenant-a", TaskID: "task-a", ArtifactID: "artifact-a",
		SHA256: "other", SizeBytes: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, ArtifactQuota{MaxBytes: 10, MaxObjects: 1}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
	if _, err := store.FinalizeArtifact(
		context.Background(), object.TenantID, object.ID, "aa/object", "text/plain",
		ArtifactScanClean, ArtifactObjectAvailable, now,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	usage, err := store.GetArtifactUsage(context.Background(), object.TenantID)
	if err != nil || usage.Bytes != 10 || usage.Objects != 1 {
		t.Fatalf("replayed usage=%+v err=%v", usage, err)
	}
	leases, err := store.ClaimExpiredArtifacts(context.Background(), "worker-a", 1, now.Add(2*time.Hour), time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("leases=%+v err=%v", leases, err)
	}
	if second, err := store.ClaimExpiredArtifacts(context.Background(), "worker-b", 1, now.Add(2*time.Hour), time.Minute); err != nil || len(second) != 0 {
		t.Fatalf("duplicate leases=%+v err=%v", second, err)
	}
	if err := store.CompleteArtifactDeletion(context.Background(), leases[0], now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	usage, _ = store.GetArtifactUsage(context.Background(), object.TenantID)
	if usage.Bytes != 0 || usage.Objects != 0 {
		t.Fatalf("completed deletion usage=%+v", usage)
	}
}

func TestJournalTaskEventIsAtomicallyEnqueuedInOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.journal")
	store, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	task, err := store.CreateTask(ctx, Task{
		ID: "task-outbox", TenantID: "tenant-a", AgentID: "agent-1",
		State: TaskStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.submitted", State: TaskStateSubmitted, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	leases, err := store.ClaimOutbox(ctx, "publisher-a", 10, now, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("outbox leases=%+v err=%v", leases, err)
	}
	if leases[0].Item.TaskID != task.ID || leases[0].Item.Topic != "task.submitted" || !json.Valid(leases[0].Item.Payload) {
		t.Fatalf("unexpected outbox item=%+v", leases[0].Item)
	}
	if second, err := store.ClaimOutbox(ctx, "publisher-b", 10, now, time.Minute); err != nil || len(second) != 0 {
		t.Fatalf("leased outbox was duplicated: %+v err=%v", second, err)
	}
	if err := store.AckOutbox(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	if third, err := store.ClaimOutbox(ctx, "publisher-c", 10, now.Add(time.Hour), time.Minute); err != nil || len(third) != 0 {
		t.Fatalf("acked outbox was redelivered: %+v err=%v", third, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if replayed, err := store.ClaimOutbox(ctx, "publisher-d", 10, now.Add(2*time.Hour), time.Minute); err != nil || len(replayed) != 0 {
		t.Fatalf("acked outbox replayed after restart: %+v err=%v", replayed, err)
	}
}

func TestJournalOutboxAdministrationIsTenantScopedAndReplayable(t *testing.T) {
	store, err := OpenJournal("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateTask(ctx, Task{ID: "task-admin", TenantID: "tenant-a", AgentID: "agent-1", State: TaskStateSubmitted, CreatedAt: now, UpdatedAt: now}, Event{Type: "task.submitted", State: TaskStateSubmitted, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	leases, err := store.ClaimOutbox(ctx, "publisher", 1, now, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("leases=%+v err=%v", leases, err)
	}
	if err := store.DeadLetterOutbox(ctx, leases[0], "collector unavailable"); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListOutbox(ctx, "tenant-a", OutboxDeadLettered, 10)
	if err != nil || len(records) != 1 || records[0].LastError != "collector unavailable" {
		t.Fatalf("dead letters=%+v err=%v", records, err)
	}
	if other, err := store.ListOutbox(ctx, "tenant-b", "", 10); err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant records=%+v err=%v", other, err)
	}
	if err := store.ReplayOutbox(ctx, "tenant-a", records[0].Item.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListOutbox(ctx, "tenant-a", OutboxPending, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("replayed records=%+v err=%v", pending, err)
	}
	leases, err = store.ClaimOutbox(ctx, "publisher-2", 1, now.Add(2*time.Minute), time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("replay claim=%+v err=%v", leases, err)
	}
	if err := store.AckOutbox(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	purged, err := store.PurgeOutbox(ctx, "tenant-a", now.Add(time.Hour), 10)
	if err != nil || purged != 1 {
		t.Fatalf("purged=%d err=%v", purged, err)
	}
	records, err = store.ListOutbox(ctx, "tenant-a", OutboxPurged, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("purged records=%+v err=%v", records, err)
	}
}
