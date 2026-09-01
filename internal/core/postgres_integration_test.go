package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresTransactionalStoreAndMultiInstanceLeases(t *testing.T) {
	dataSourceName := os.Getenv("AFH_TEST_POSTGRES_DSN")
	if dataSourceName == "" {
		t.Skip("AFH_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	first, err := OpenPostgres(ctx, dataSourceName)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenPostgres(ctx, dataSourceName)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var migrationCount int
	if err := first.pool.QueryRow(ctx, `SELECT count(*) FROM afh_schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount < 4 {
		t.Fatalf("schema migration ledger rows=%d, want at least 4", migrationCount)
	}
	if err := second.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration check failed: %v", err)
	}
	if _, err := first.pool.Exec(ctx, `
		TRUNCATE afh_artifacts, afh_artifact_usage, afh_token_revocations,
			afh_workflow_events, afh_workflows, afh_events, afh_outbox, afh_inbox,
			afh_tasks, afh_agents CASCADE`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	workflow, err := first.CreateWorkflow(ctx, Workflow{
		ID: "workflow-1", TenantID: "tenant-a", Name: "durable-workflow",
		State: WorkflowStateRunning, Steps: []WorkflowStep{{ID: "step-1", State: TaskStateWorking}},
		CreatedAt: now, UpdatedAt: now,
	}, WorkflowEvent{Type: "workflow.created", State: WorkflowStateRunning, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	updatedWorkflow, applied, err := first.ApplyWorkflowVersion(ctx, workflow.TenantID, workflow.ID, workflow.Revision, "workflow-progress", func(workflow *Workflow) (WorkflowEvent, error) {
		workflow.State = WorkflowStatePartiallyFailed
		workflow.Steps[0].State = TaskStateCompleted
		workflow.UpdatedAt = now.Add(time.Second)
		return WorkflowEvent{Type: "workflow.step.status", State: workflow.State, StepID: "step-1", CreatedAt: workflow.UpdatedAt}, nil
	})
	if err != nil || !applied {
		t.Fatalf("workflow mutation applied=%v err=%v", applied, err)
	}
	duplicateWorkflow, applied, err := second.ApplyWorkflowVersion(ctx, workflow.TenantID, workflow.ID, 0, "workflow-progress", func(workflow *Workflow) (WorkflowEvent, error) {
		workflow.State = WorkflowStateFailed
		return WorkflowEvent{Type: "must-not-apply", CreatedAt: now}, nil
	})
	if err != nil || applied || duplicateWorkflow.Revision != updatedWorkflow.Revision {
		t.Fatalf("workflow duplicate=%+v applied=%v err=%v", duplicateWorkflow, applied, err)
	}
	persistedWorkflow, err := second.GetWorkflow(ctx, workflow.TenantID, workflow.ID)
	if err != nil || persistedWorkflow.State != WorkflowStatePartiallyFailed {
		t.Fatalf("workflow persistence=%+v err=%v", persistedWorkflow, err)
	}
	if err := first.PutAgent(ctx, Agent{
		ID: "agent-1", TenantID: "tenant-a", Name: "remote",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	task, err := first.CreateTask(ctx, Task{
		ID: "task-1", TenantID: "tenant-a", AgentID: "agent-1", RemoteTaskID: "remote-1",
		State: TaskStateWorking, CreatedAt: now, UpdatedAt: now,
	}, Event{Type: "task.status", State: TaskStateWorking, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	mutationError := errors.New("rollback requested")
	_, applied, err = first.ApplyTaskVersion(ctx, task.TenantID, task.ID, task.Revision, "rollback", func(task *Task) (Event, error) {
		task.State = TaskStateFailed
		return Event{Type: "task.status", CreatedAt: now}, mutationError
	})
	if !errors.Is(err, mutationError) || applied {
		t.Fatalf("rollback mutation applied=%v err=%v", applied, err)
	}
	current, err := second.GetTask(ctx, task.TenantID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != task.Revision || current.State != TaskStateWorking {
		t.Fatalf("rolled back Task changed: %+v", current)
	}
	if _, applied, err := first.ApplyTaskVersion(ctx, task.TenantID, task.ID, task.Revision+1, "stale", func(task *Task) (Event, error) {
		return Event{Type: "task.status", CreatedAt: now}, nil
	}); !errors.Is(err, ErrRevisionConflict) || applied {
		t.Fatalf("stale PostgreSQL revision applied=%v err=%v", applied, err)
	}
	updated, applied, err := first.ApplyTaskVersion(ctx, task.TenantID, task.ID, task.Revision, "observation-1", func(task *Task) (Event, error) {
		task.UpdatedAt = now.Add(time.Second)
		return Event{Type: "task.status", State: task.State, CreatedAt: task.UpdatedAt}, nil
	})
	if err != nil || !applied {
		t.Fatalf("PostgreSQL mutation applied=%v err=%v", applied, err)
	}
	duplicate, applied, err := second.ApplyTask(ctx, task.TenantID, task.ID, "observation-1", func(task *Task) (Event, error) {
		return Event{Type: "must-not-apply", CreatedAt: now}, nil
	})
	if err != nil || applied || duplicate.Revision != updated.Revision {
		t.Fatalf("PostgreSQL duplicate=%+v applied=%v err=%v", duplicate, applied, err)
	}
	events, err := second.EventsAfter(ctx, task.TenantID, task.ID, 0)
	if err != nil || len(events) != 2 {
		t.Fatalf("rolled back Event persisted: events=%+v err=%v", events, err)
	}
	outboxA, err := first.ClaimOutbox(ctx, "publisher-a", 10, now, time.Minute)
	if err != nil || len(outboxA) != 2 || outboxA[0].Item.Topic != "task.status" {
		t.Fatalf("transactional outbox claim=%+v err=%v", outboxA, err)
	}
	outboxB, err := second.ClaimOutbox(ctx, "publisher-b", 10, now, time.Minute)
	if err != nil || len(outboxB) != 0 {
		t.Fatalf("outbox lease duplicated=%+v err=%v", outboxB, err)
	}
	for _, lease := range outboxA {
		if err := second.AckOutbox(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	deadLetterItem := OutboxItem{
		ID: "outbox-dead-letter", TenantID: task.TenantID, TaskID: task.ID,
		DedupKey: "dead-letter", Topic: "task.status", Payload: json.RawMessage(`{"state":"FAILED"}`), CreatedAt: now,
	}
	if created, err := first.EnqueueOutbox(ctx, deadLetterItem); err != nil || !created {
		t.Fatalf("dead-letter enqueue created=%v err=%v", created, err)
	}
	deadLetterLease, err := first.ClaimOutbox(ctx, "publisher-dlq", 1, now, time.Minute)
	if err != nil || len(deadLetterLease) != 1 {
		t.Fatalf("dead-letter claim=%+v err=%v", deadLetterLease, err)
	}
	if err := first.DeadLetterOutbox(ctx, deadLetterLease[0], "permanent collector failure"); err != nil {
		t.Fatal(err)
	}
	if claim, err := second.ClaimOutbox(ctx, "publisher-retry", 1, now.Add(time.Hour), time.Minute); err != nil || len(claim) != 0 {
		t.Fatalf("dead-lettered outbox was claimable: %+v err=%v", claim, err)
	}
	var lastError string
	if err := first.pool.QueryRow(ctx, `SELECT last_error FROM afh_outbox WHERE id=$1`, deadLetterItem.ID).Scan(&lastError); err != nil || lastError != "permanent collector failure" {
		t.Fatalf("dead-letter reason=%q err=%v", lastError, err)
	}

	start := make(chan struct{})
	results := make(chan []WorkLease, 2)
	errorsOut := make(chan error, 2)
	var wait sync.WaitGroup
	for index, store := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func(index int, store *PostgresStore) {
			defer wait.Done()
			<-start
			leases, err := store.ClaimRecoverable(ctx, []string{"worker-a", "worker-b"}[index], 1, now, time.Minute)
			results <- leases
			errorsOut <- err
		}(index, store)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsOut)
	claimed := 0
	var lease WorkLease
	for leases := range results {
		claimed += len(leases)
		if len(leases) == 1 {
			lease = leases[0]
		}
	}
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if claimed != 1 {
		t.Fatalf("multi-instance lease claims=%d, want 1", claimed)
	}

	takenOver, err := second.ClaimRecoverable(ctx, "worker-c", 1, now.Add(time.Minute+time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(takenOver) != 1 || takenOver[0].Task.ID != lease.Task.ID || takenOver[0].Attempt != 2 {
		t.Fatalf("expired lease takeover=%+v", takenOver)
	}
	if err := first.ReleaseLease(ctx, lease, now, true); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale owner release error=%v", err)
	}

	item := InboxItem{
		ID: "inbox-1", TenantID: task.TenantID, TaskID: task.ID,
		DedupKey: "push-1", Protocol: "a2a", Payload: json.RawMessage(`{"state":"COMPLETED"}`), CreatedAt: now,
	}
	created, err := first.EnqueueInbox(ctx, item)
	if err != nil || !created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	created, err = second.EnqueueInbox(ctx, InboxItem{
		ID: "inbox-duplicate", TenantID: task.TenantID, TaskID: task.ID,
		DedupKey: item.DedupKey, Protocol: item.Protocol, Payload: item.Payload, CreatedAt: now,
	})
	if err != nil || created {
		t.Fatalf("duplicate inbox created=%v err=%v", created, err)
	}
	inboxA, err := first.ClaimInbox(ctx, "inbox-worker-a", 1, now, time.Minute)
	if err != nil || len(inboxA) != 1 {
		t.Fatalf("first inbox claim=%+v err=%v", inboxA, err)
	}
	inboxB, err := second.ClaimInbox(ctx, "inbox-worker-b", 1, now, time.Minute)
	if err != nil || len(inboxB) != 0 {
		t.Fatalf("duplicate inbox claim=%+v err=%v", inboxB, err)
	}
	if err := second.AckInbox(ctx, inboxA[0]); err != nil {
		t.Fatal(err)
	}
	if pending, err := first.ClaimInbox(ctx, "inbox-worker-c", 1, now.Add(2*time.Minute), time.Minute); err != nil || len(pending) != 0 {
		t.Fatalf("acked inbox redelivered=%+v err=%v", pending, err)
	}

	revocation := TokenRevocation{
		Issuer: "https://issuer.example", TokenID: "token-a", TenantID: task.TenantID,
		RevokedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := first.RevokeToken(ctx, revocation); err != nil {
		t.Fatal(err)
	}
	if revoked, err := second.TokenRevoked(ctx, revocation.Issuer, revocation.TokenID, revocation.TenantID, now); err != nil || !revoked {
		t.Fatalf("PostgreSQL revocation active=%v err=%v", revoked, err)
	}

	object := ArtifactObject{
		ID: "object-a", TenantID: task.TenantID, TaskID: task.ID, ArtifactID: "artifact-a",
		PartIndex: 0, SHA256: "digest-a", SizeBytes: 10,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	start = make(chan struct{})
	type reservationResult struct {
		object  ArtifactObject
		created bool
		err     error
	}
	reservations := make(chan reservationResult, 2)
	wait = sync.WaitGroup{}
	for _, store := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func(store *PostgresStore) {
			defer wait.Done()
			<-start
			reserved, created, err := store.ReserveArtifact(ctx, object, ArtifactQuota{MaxBytes: 10, MaxObjects: 1})
			reservations <- reservationResult{object: reserved, created: created, err: err}
		}(store)
	}
	close(start)
	wait.Wait()
	close(reservations)
	createdCount := 0
	for result := range reservations {
		if result.err != nil || result.object.ID != object.ID {
			t.Fatalf("reservation object=%+v created=%v error=%v", result.object, result.created, result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created Artifact reservations=%d, want 1", createdCount)
	}
	usage, err := second.GetArtifactUsage(ctx, task.TenantID)
	if err != nil || usage.Bytes != object.SizeBytes || usage.Objects != 1 {
		t.Fatalf("PostgreSQL Artifact usage=%+v err=%v", usage, err)
	}
	if _, err := first.FinalizeArtifact(
		ctx, object.TenantID, object.ID, "aa/object-a", "text/plain",
		ArtifactScanClean, ArtifactObjectAvailable, now,
	); err != nil {
		t.Fatal(err)
	}
	artifactLeasesA, err := first.ClaimExpiredArtifacts(ctx, "artifact-worker-a", 1, now.Add(2*time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	artifactLeasesB, err := second.ClaimExpiredArtifacts(ctx, "artifact-worker-b", 1, now.Add(2*time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactLeasesA)+len(artifactLeasesB) != 1 {
		t.Fatalf("multi-instance Artifact leases: first=%+v second=%+v", artifactLeasesA, artifactLeasesB)
	}
	artifactLease := append(artifactLeasesA, artifactLeasesB...)[0]
	if err := second.CompleteArtifactDeletion(ctx, artifactLease, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	usage, err = first.GetArtifactUsage(ctx, task.TenantID)
	if err != nil || usage.Bytes != 0 || usage.Objects != 0 {
		t.Fatalf("deleted PostgreSQL Artifact usage=%+v err=%v", usage, err)
	}
}
