package core

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var postgresMigrations embed.FS

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgres(ctx context.Context, dataSourceName string) (*PostgresStore, error) {
	config, err := pgxpool.ParseConfig(dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	entries, err := postgresMigrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("list PostgreSQL migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS afh_schema_migrations (
			name TEXT PRIMARY KEY,
			checksum BYTEA NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		migration, err := postgresMigrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read PostgreSQL migration %s: %w", entry.Name(), err)
		}
		checksum := sha256.Sum256(migration)
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin PostgreSQL migration %s: %w", entry.Name(), err)
		}
		var applied []byte
		err = tx.QueryRow(ctx, `
			SELECT checksum FROM afh_schema_migrations WHERE name=$1 FOR UPDATE`, entry.Name()).Scan(&applied)
		if err == nil {
			if string(applied) != string(checksum[:]) {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("PostgreSQL migration %s checksum changed", entry.Name())
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit PostgreSQL migration %s ledger check: %w", entry.Name(), err)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("read PostgreSQL migration ledger %s: %w", entry.Name(), err)
		}
		// Serialize migration runners while keeping DDL and its ledger row atomic.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent-federation-hub:schema'))`); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("lock PostgreSQL migration %s: %w", entry.Name(), err)
		}
		// A concurrent runner may have applied this migration while this
		// transaction waited for the advisory lock, so check the ledger again.
		if err := tx.QueryRow(ctx, `
			SELECT checksum FROM afh_schema_migrations WHERE name=$1`, entry.Name()).Scan(&applied); err == nil {
			if string(applied) != string(checksum[:]) {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("PostgreSQL migration %s checksum changed", entry.Name())
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit PostgreSQL migration %s ledger check: %w", entry.Name(), err)
			}
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recheck PostgreSQL migration ledger %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply PostgreSQL migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO afh_schema_migrations (name, checksum) VALUES ($1, $2)`, entry.Name(), checksum[:]); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record PostgreSQL migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit PostgreSQL migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *PostgresStore) PutAgent(ctx context.Context, agent Agent) error {
	clone, err := CloneAgent(agent)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(clone)
	if err != nil {
		return fmt.Errorf("encode Agent: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO afh_agents (tenant_id, id, payload, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, id) DO UPDATE
		SET payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at`,
		agent.TenantID, agent.ID, payload, agent.UpdatedAt)
	return err
}

func (s *PostgresStore) GetAgent(ctx context.Context, tenantID, id string) (Agent, error) {
	var payload []byte
	if err := s.pool.QueryRow(ctx, `SELECT payload FROM afh_agents WHERE tenant_id=$1 AND id=$2`, tenantID, id).Scan(&payload); err != nil {
		return Agent{}, mapPostgresNotFound(err)
	}
	var agent Agent
	if err := json.Unmarshal(payload, &agent); err != nil {
		return Agent{}, fmt.Errorf("decode stored Agent: %w", err)
	}
	return agent, nil
}

func (s *PostgresStore) ListAgents(ctx context.Context, tenantID string) ([]Agent, error) {
	rows, err := s.pool.Query(ctx, `SELECT payload FROM afh_agents WHERE tenant_id=$1 ORDER BY id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Agent, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var agent Agent
		if err := json.Unmarshal(payload, &agent); err != nil {
			return nil, fmt.Errorf("decode stored Agent: %w", err)
		}
		result = append(result, agent)
	}
	return result, rows.Err()
}

func (s *PostgresStore) CreateTask(ctx context.Context, task Task, event Event) (Task, error) {
	task.Revision = 1
	task.LastSequence = 1
	event.ID = NewID()
	event.TaskID = task.ID
	event.TenantID = task.TenantID
	event.Sequence = 1
	if event.DedupKey == "" {
		event.DedupKey = "local:" + event.ID
	}
	taskPayload, eventPayload, err := encodeTaskAndEvent(task, event)
	if err != nil {
		return Task{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		INSERT INTO afh_tasks (tenant_id, id, payload, revision, state, remote_task_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		task.TenantID, task.ID, taskPayload, task.Revision, task.State, task.RemoteTaskID, task.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Task{}, ErrConflict
		}
		return Task{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO afh_events (tenant_id, task_id, sequence, dedup_key, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		event.TenantID, event.TaskID, event.Sequence, event.DedupKey, eventPayload, event.CreatedAt); err != nil {
		return Task{}, err
	}
	outbox, err := outboxFromEvent(event)
	if err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO afh_outbox (id, tenant_id, task_id, dedup_key, topic, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		outbox.ID, outbox.TenantID, outbox.TaskID, outbox.DedupKey, outbox.Topic, outbox.Payload, outbox.CreatedAt); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *PostgresStore) ApplyTask(
	ctx context.Context,
	tenantID string,
	id string,
	dedupKey string,
	mutate func(*Task) (Event, error),
) (Task, bool, error) {
	return s.ApplyTaskVersion(ctx, tenantID, id, 0, dedupKey, mutate)
}

func (s *PostgresStore) ApplyTaskVersion(
	ctx context.Context,
	tenantID string,
	id string,
	expectedRevision uint64,
	dedupKey string,
	mutate func(*Task) (Event, error),
) (Task, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback(ctx)
	var payload []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM afh_tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, id).Scan(&payload); err != nil {
		return Task{}, false, mapPostgresNotFound(err)
	}
	var current Task
	if err := json.Unmarshal(payload, &current); err != nil {
		return Task{}, false, fmt.Errorf("decode stored Task: %w", err)
	}
	if expectedRevision != 0 && current.Revision != expectedRevision {
		return current, false, ErrRevisionConflict
	}
	if dedupKey != "" {
		var duplicate bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM afh_events WHERE tenant_id=$1 AND task_id=$2 AND dedup_key=$3)`,
			tenantID, id, dedupKey).Scan(&duplicate); err != nil {
			return Task{}, false, err
		}
		if duplicate {
			return current, false, nil
		}
	}
	updated, err := CloneTask(current)
	if err != nil {
		return Task{}, false, err
	}
	event, err := mutate(&updated)
	if err != nil {
		return Task{}, false, err
	}
	updated.Revision++
	updated.LastSequence++
	event.ID = NewID()
	event.TaskID = updated.ID
	event.TenantID = updated.TenantID
	event.Sequence = updated.LastSequence
	event.DedupKey = dedupKey
	if event.DedupKey == "" {
		event.DedupKey = "local:" + event.ID
	}
	taskPayload, eventPayload, err := encodeTaskAndEvent(updated, event)
	if err != nil {
		return Task{}, false, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE afh_tasks SET payload=$1, revision=$2, state=$3, remote_task_id=$4, updated_at=$5
		WHERE tenant_id=$6 AND id=$7 AND revision=$8`,
		taskPayload, updated.Revision, updated.State, updated.RemoteTaskID, updated.UpdatedAt,
		tenantID, id, current.Revision)
	if err != nil {
		return Task{}, false, err
	}
	if command.RowsAffected() != 1 {
		return current, false, ErrRevisionConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO afh_events (tenant_id, task_id, sequence, dedup_key, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		event.TenantID, event.TaskID, event.Sequence, event.DedupKey, eventPayload, event.CreatedAt); err != nil {
		return Task{}, false, err
	}
	outbox, err := outboxFromEvent(event)
	if err != nil {
		return Task{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO afh_outbox (id, tenant_id, task_id, dedup_key, topic, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		outbox.ID, outbox.TenantID, outbox.TaskID, outbox.DedupKey, outbox.Topic, outbox.Payload, outbox.CreatedAt); err != nil {
		return Task{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, false, err
	}
	return updated, true, nil
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID, id string) (Task, error) {
	var payload []byte
	if err := s.pool.QueryRow(ctx, `SELECT payload FROM afh_tasks WHERE tenant_id=$1 AND id=$2`, tenantID, id).Scan(&payload); err != nil {
		return Task{}, mapPostgresNotFound(err)
	}
	var task Task
	if err := json.Unmarshal(payload, &task); err != nil {
		return Task{}, fmt.Errorf("decode stored Task: %w", err)
	}
	return task, nil
}

func (s *PostgresStore) ListRecoverable(ctx context.Context) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT payload FROM afh_tasks
		WHERE remote_task_id <> '' AND state NOT IN ('COMPLETED', 'FAILED', 'CANCELED', 'REJECTED')
		ORDER BY tenant_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *PostgresStore) EventsAfter(ctx context.Context, tenantID, id string, after uint64) ([]Event, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM afh_tasks WHERE tenant_id=$1 AND id=$2)`, tenantID, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT payload FROM afh_events WHERE tenant_id=$1 AND task_id=$2 AND sequence>$3 ORDER BY sequence`,
		tenantID, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Event, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("decode stored Event: %w", err)
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *PostgresStore) ClaimRecoverable(
	ctx context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]WorkLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("lease owner, positive limit, and duration are required")
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT tenant_id, id FROM afh_tasks
			WHERE remote_task_id <> ''
			  AND state NOT IN ('COMPLETED', 'FAILED', 'CANCELED', 'REJECTED')
			  AND available_at <= $1
			  AND (lease_owner = '' OR lease_expires_at <= $1)
			ORDER BY available_at, updated_at, tenant_id, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE afh_tasks AS task
		SET lease_owner=$3, lease_expires_at=$4, reconcile_attempts=task.reconcile_attempts+1
		FROM candidates
		WHERE task.tenant_id=candidates.tenant_id AND task.id=candidates.id
		RETURNING task.payload, task.lease_expires_at, task.reconcile_attempts`,
		now, limit, owner, now.Add(duration))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkLease, 0)
	for rows.Next() {
		var payload []byte
		var expiresAt time.Time
		var attempt uint32
		if err := rows.Scan(&payload, &expiresAt, &attempt); err != nil {
			return nil, err
		}
		var task Task
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, fmt.Errorf("decode leased Task: %w", err)
		}
		result = append(result, WorkLease{Task: task, Owner: owner, ExpiresAt: expiresAt, Attempt: attempt})
	}
	return result, rows.Err()
}

func (s *PostgresStore) RenewLease(
	ctx context.Context,
	lease WorkLease,
	now time.Time,
	duration time.Duration,
) (WorkLease, error) {
	expiresAt := now.Add(duration)
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_tasks SET lease_expires_at=$1
		WHERE tenant_id=$2 AND id=$3 AND lease_owner=$4 AND lease_expires_at>$5`,
		expiresAt, lease.Task.TenantID, lease.Task.ID, lease.Owner, now)
	if err != nil {
		return WorkLease{}, err
	}
	if command.RowsAffected() != 1 {
		return WorkLease{}, ErrLeaseLost
	}
	lease.ExpiresAt = expiresAt
	return lease, nil
}

func (s *PostgresStore) ReleaseLease(
	ctx context.Context,
	lease WorkLease,
	availableAt time.Time,
	resetAttempts bool,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_tasks
		SET lease_owner='', lease_expires_at=NULL, available_at=$1,
			reconcile_attempts=CASE WHEN $2 THEN 0 ELSE reconcile_attempts END
		WHERE tenant_id=$3 AND id=$4 AND lease_owner=$5`,
		availableAt, resetAttempts, lease.Task.TenantID, lease.Task.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) EnqueueInbox(ctx context.Context, item InboxItem) (bool, error) {
	if item.ID == "" || item.TenantID == "" || item.TaskID == "" || item.DedupKey == "" {
		return false, errors.New("inbox ID, tenant, Task, and dedup key are required")
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO afh_inbox (id, tenant_id, task_id, dedup_key, payload, protocol, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, task_id, dedup_key) DO NOTHING`,
		item.ID, item.TenantID, item.TaskID, item.DedupKey, item.Payload, item.Protocol, item.CreatedAt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (s *PostgresStore) ClaimInbox(
	ctx context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]InboxLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("inbox lease owner, positive limit, and duration are required")
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM afh_inbox
			WHERE acked_at IS NULL AND available_at <= $1
			  AND (lease_owner = '' OR lease_expires_at <= $1)
			ORDER BY available_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE afh_inbox AS inbox
		SET lease_owner=$3, lease_expires_at=$4, attempts=inbox.attempts+1
		FROM candidates
		WHERE inbox.id=candidates.id
		RETURNING inbox.id, inbox.tenant_id, inbox.task_id, inbox.dedup_key,
			inbox.protocol, inbox.payload, inbox.created_at, inbox.lease_expires_at, inbox.attempts`,
		now, limit, owner, now.Add(duration))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]InboxLease, 0)
	for rows.Next() {
		var item InboxItem
		var expiresAt time.Time
		var attempt uint32
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.TaskID, &item.DedupKey,
			&item.Protocol, &item.Payload, &item.CreatedAt, &expiresAt, &attempt,
		); err != nil {
			return nil, err
		}
		result = append(result, InboxLease{Item: item, Owner: owner, ExpiresAt: expiresAt, Attempt: attempt})
	}
	return result, rows.Err()
}

func (s *PostgresStore) RenewInboxLease(
	ctx context.Context,
	lease InboxLease,
	now time.Time,
	duration time.Duration,
) (InboxLease, error) {
	expiresAt := now.Add(duration)
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_inbox SET lease_expires_at=$1
		WHERE id=$2 AND lease_owner=$3 AND lease_expires_at>$4 AND acked_at IS NULL`,
		expiresAt, lease.Item.ID, lease.Owner, now)
	if err != nil {
		return InboxLease{}, err
	}
	if command.RowsAffected() != 1 {
		return InboxLease{}, ErrLeaseLost
	}
	lease.ExpiresAt = expiresAt
	return lease, nil
}

func (s *PostgresStore) AckInbox(ctx context.Context, lease InboxLease) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_inbox SET acked_at=now(), lease_owner='', lease_expires_at=NULL
		WHERE id=$1 AND lease_owner=$2 AND acked_at IS NULL`, lease.Item.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) RetryInbox(ctx context.Context, lease InboxLease, availableAt time.Time) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_inbox SET available_at=$1, lease_owner='', lease_expires_at=NULL
		WHERE id=$2 AND lease_owner=$3 AND acked_at IS NULL`, availableAt, lease.Item.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) EnqueueOutbox(ctx context.Context, item OutboxItem) (bool, error) {
	if item.ID == "" || item.TenantID == "" || item.TaskID == "" || item.DedupKey == "" || item.Topic == "" {
		return false, errors.New("outbox ID, tenant, Task, dedup key, and topic are required")
	}
	if len(item.Payload) == 0 || !json.Valid(item.Payload) {
		return false, errors.New("outbox payload must be valid JSON")
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO afh_outbox (id, tenant_id, task_id, dedup_key, topic, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, task_id, dedup_key) DO NOTHING`,
		item.ID, item.TenantID, item.TaskID, item.DedupKey, item.Topic, item.Payload, item.CreatedAt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (s *PostgresStore) ClaimOutbox(
	ctx context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]OutboxLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("outbox lease owner, positive limit, and duration are required")
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM afh_outbox
			WHERE acked_at IS NULL AND dead_lettered_at IS NULL AND available_at <= $1
			  AND (lease_owner = '' OR lease_expires_at <= $1)
			ORDER BY available_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE afh_outbox AS outbox
		SET lease_owner=$3, lease_expires_at=$4, attempts=outbox.attempts+1
		FROM candidates
		WHERE outbox.id=candidates.id
		RETURNING outbox.id, outbox.tenant_id, outbox.task_id, outbox.dedup_key,
			outbox.topic, outbox.payload, outbox.created_at, outbox.lease_expires_at, outbox.attempts`,
		now, limit, owner, now.Add(duration))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OutboxLease, 0)
	for rows.Next() {
		var item OutboxItem
		var expiresAt time.Time
		var attempt uint32
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.TaskID, &item.DedupKey,
			&item.Topic, &item.Payload, &item.CreatedAt, &expiresAt, &attempt,
		); err != nil {
			return nil, err
		}
		result = append(result, OutboxLease{Item: item, Owner: owner, ExpiresAt: expiresAt, Attempt: attempt})
	}
	return result, rows.Err()
}

func (s *PostgresStore) RenewOutboxLease(
	ctx context.Context,
	lease OutboxLease,
	now time.Time,
	duration time.Duration,
) (OutboxLease, error) {
	expiresAt := now.Add(duration)
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_outbox SET lease_expires_at=$1
		WHERE id=$2 AND lease_owner=$3 AND lease_expires_at>$4 AND acked_at IS NULL`,
		expiresAt, lease.Item.ID, lease.Owner, now)
	if err != nil {
		return OutboxLease{}, err
	}
	if command.RowsAffected() != 1 {
		return OutboxLease{}, ErrLeaseLost
	}
	lease.ExpiresAt = expiresAt
	return lease, nil
}

func (s *PostgresStore) AckOutbox(ctx context.Context, lease OutboxLease) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_outbox SET acked_at=now(), lease_owner='', lease_expires_at=NULL
		WHERE id=$1 AND lease_owner=$2 AND acked_at IS NULL`, lease.Item.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) RetryOutbox(ctx context.Context, lease OutboxLease, availableAt time.Time) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_outbox SET available_at=$1, lease_owner='', lease_expires_at=NULL
		WHERE id=$2 AND lease_owner=$3 AND acked_at IS NULL AND dead_lettered_at IS NULL`, availableAt, lease.Item.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) DeadLetterOutbox(ctx context.Context, lease OutboxLease, reason string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_outbox
		SET dead_lettered_at=now(), last_error=$1, lease_owner='', lease_expires_at=NULL
		WHERE id=$2 AND lease_owner=$3 AND acked_at IS NULL AND dead_lettered_at IS NULL`,
		reason, lease.Item.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) RevokeToken(ctx context.Context, revocation TokenRevocation) error {
	if revocation.Issuer == "" || revocation.TokenID == "" || revocation.TenantID == "" ||
		revocation.RevokedAt.IsZero() || revocation.ExpiresAt.IsZero() {
		return errors.New("revocation issuer, token ID, tenant, revoked time, and expiry are required")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO afh_token_revocations (issuer, token_id, tenant_id, reason, revoked_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (issuer, token_id, tenant_id) DO UPDATE
		SET reason=EXCLUDED.reason,
			revoked_at=LEAST(afh_token_revocations.revoked_at, EXCLUDED.revoked_at),
			expires_at=GREATEST(afh_token_revocations.expires_at, EXCLUDED.expires_at)`,
		revocation.Issuer, revocation.TokenID, revocation.TenantID,
		revocation.Reason, revocation.RevokedAt, revocation.ExpiresAt)
	return err
}

func (s *PostgresStore) TokenRevoked(
	ctx context.Context,
	issuer string,
	tokenID string,
	tenantID string,
	now time.Time,
) (bool, error) {
	var revoked bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM afh_token_revocations
			WHERE issuer=$1 AND token_id=$2 AND tenant_id=$3 AND expires_at>$4
		)`, issuer, tokenID, tenantID, now).Scan(&revoked)
	return revoked, err
}

func (s *PostgresStore) PruneRevocations(ctx context.Context, now time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, `DELETE FROM afh_token_revocations WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func (s *PostgresStore) ReserveArtifact(
	ctx context.Context,
	object ArtifactObject,
	quota ArtifactQuota,
) (ArtifactObject, bool, error) {
	if err := validateArtifactReservation(object); err != nil {
		return ArtifactObject{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ArtifactObject{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO afh_artifact_usage (tenant_id) VALUES ($1)
		ON CONFLICT (tenant_id) DO NOTHING`, object.TenantID); err != nil {
		return ArtifactObject{}, false, err
	}
	var usage ArtifactUsage
	usage.TenantID = object.TenantID
	if err := tx.QueryRow(ctx, `
		SELECT bytes, objects FROM afh_artifact_usage WHERE tenant_id=$1 FOR UPDATE`,
		object.TenantID).Scan(&usage.Bytes, &usage.Objects); err != nil {
		return ArtifactObject{}, false, err
	}
	existing, found, err := getArtifactRow(ctx, tx, object.TenantID, object.ID, true)
	if err != nil {
		return ArtifactObject{}, false, err
	}
	if found {
		if !sameArtifactIdentity(existing, object) {
			return ArtifactObject{}, false, ErrConflict
		}
		if existing.Status != ArtifactObjectFailed {
			return existing, false, nil
		}
	}
	if (quota.MaxBytes > 0 && usage.Bytes+object.SizeBytes > quota.MaxBytes) ||
		(quota.MaxObjects > 0 && usage.Objects+1 > quota.MaxObjects) {
		return ArtifactObject{}, false, ErrQuotaExceeded
	}
	object.Status = ArtifactObjectPending
	object.ScanStatus = ArtifactScanNotScanned
	object.StorageKey = ""
	object.FailureCode = ""
	object.DeletedAt = nil
	if found {
		_, err = tx.Exec(ctx, `
			UPDATE afh_artifacts SET storage_key='', detected_media_type='', status=$1, scan_status=$2,
				updated_at=$3, deleted_at=NULL, failure_code='', lease_owner='', lease_expires_at=NULL,
				available_at='-infinity'
			WHERE tenant_id=$4 AND id=$5`,
			object.Status, object.ScanStatus, object.UpdatedAt, object.TenantID, object.ID)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO afh_artifacts (
				tenant_id, id, task_id, artifact_id, part_index, sha256, size_bytes,
				declared_media_type, filename, source_uri, status, scan_status,
				created_at, updated_at, expires_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			object.TenantID, object.ID, object.TaskID, object.ArtifactID, object.PartIndex,
			object.SHA256, object.SizeBytes, object.DeclaredMediaType, object.Filename,
			object.SourceURI, object.Status, object.ScanStatus, object.CreatedAt,
			object.UpdatedAt, object.ExpiresAt)
	}
	if err != nil {
		if isUniqueViolation(err) {
			return ArtifactObject{}, false, ErrConflict
		}
		return ArtifactObject{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE afh_artifact_usage SET bytes=bytes+$1, objects=objects+1 WHERE tenant_id=$2`,
		object.SizeBytes, object.TenantID); err != nil {
		return ArtifactObject{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ArtifactObject{}, false, err
	}
	return object, true, nil
}

func (s *PostgresStore) FinalizeArtifact(
	ctx context.Context,
	tenantID, id, storageKey, detectedMediaType string,
	scanStatus ArtifactScanStatus,
	status ArtifactObjectStatus,
	now time.Time,
) (ArtifactObject, error) {
	if storageKey == "" || (status != ArtifactObjectAvailable && status != ArtifactObjectQuarantined) {
		return ArtifactObject{}, errors.New("storage key and a final available or quarantined status are required")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_artifacts
		SET storage_key=$1, detected_media_type=$2, scan_status=$3, status=$4, updated_at=$5
		WHERE tenant_id=$6 AND id=$7 AND status='PENDING'`,
		storageKey, detectedMediaType, scanStatus, status, now, tenantID, id)
	if err != nil {
		return ArtifactObject{}, err
	}
	if command.RowsAffected() != 1 {
		existing, getErr := s.GetArtifact(ctx, tenantID, id)
		if getErr != nil {
			return ArtifactObject{}, getErr
		}
		if existing.Status != status || existing.StorageKey != storageKey {
			return ArtifactObject{}, ErrConflict
		}
		return existing, nil
	}
	return s.GetArtifact(ctx, tenantID, id)
}

func (s *PostgresStore) FailArtifact(
	ctx context.Context,
	tenantID, id, failureCode string,
	now time.Time,
) (ArtifactObject, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ArtifactObject{}, err
	}
	defer tx.Rollback(ctx)
	object, found, err := getArtifactRow(ctx, tx, tenantID, id, true)
	if err != nil {
		return ArtifactObject{}, err
	}
	if !found {
		return ArtifactObject{}, ErrNotFound
	}
	if object.Status == ArtifactObjectFailed {
		return object, nil
	}
	if object.Status != ArtifactObjectPending {
		return ArtifactObject{}, ErrConflict
	}
	object.Status = ArtifactObjectFailed
	object.FailureCode = failureCode
	object.UpdatedAt = now
	if _, err := tx.Exec(ctx, `
		UPDATE afh_artifacts SET status=$1, failure_code=$2, updated_at=$3
		WHERE tenant_id=$4 AND id=$5`, object.Status, failureCode, now, tenantID, id); err != nil {
		return ArtifactObject{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE afh_artifact_usage SET bytes=bytes-$1, objects=objects-1
		WHERE tenant_id=$2 AND bytes >= $1 AND objects > 0`, object.SizeBytes, tenantID)
	if err != nil {
		return ArtifactObject{}, err
	}
	if command.RowsAffected() != 1 {
		return ArtifactObject{}, errors.New("artifact usage invariant violated")
	}
	if err := tx.Commit(ctx); err != nil {
		return ArtifactObject{}, err
	}
	return object, nil
}

func (s *PostgresStore) GetArtifact(ctx context.Context, tenantID, id string) (ArtifactObject, error) {
	object, found, err := getArtifactRow(ctx, s.pool, tenantID, id, false)
	if err != nil {
		return ArtifactObject{}, err
	}
	if !found {
		return ArtifactObject{}, ErrNotFound
	}
	return object, nil
}

func (s *PostgresStore) GetArtifactUsage(ctx context.Context, tenantID string) (ArtifactUsage, error) {
	usage := ArtifactUsage{TenantID: tenantID}
	err := s.pool.QueryRow(ctx, `
		SELECT bytes, objects FROM afh_artifact_usage WHERE tenant_id=$1`, tenantID).Scan(&usage.Bytes, &usage.Objects)
	if errors.Is(err, pgx.ErrNoRows) {
		return usage, nil
	}
	return usage, err
}

func (s *PostgresStore) ClaimExpiredArtifacts(
	ctx context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]ArtifactDeletionLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("artifact lease owner, positive limit, and duration are required")
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT tenant_id, id FROM afh_artifacts
			WHERE status IN ('AVAILABLE', 'QUARANTINED', 'DELETING')
			  AND expires_at <= $1 AND available_at <= $1
			  AND (lease_owner='' OR lease_expires_at <= $1)
			ORDER BY available_at, expires_at, tenant_id, id
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE afh_artifacts AS artifact
		SET status='DELETING', updated_at=$1, lease_owner=$3, lease_expires_at=$4,
			delete_attempts=artifact.delete_attempts+1
		FROM candidates
		WHERE artifact.tenant_id=candidates.tenant_id AND artifact.id=candidates.id
		RETURNING artifact.tenant_id, artifact.id, artifact.task_id, artifact.artifact_id,
			artifact.part_index, artifact.storage_key, artifact.sha256, artifact.size_bytes,
			artifact.declared_media_type, artifact.detected_media_type, artifact.filename,
			artifact.source_uri, artifact.status, artifact.scan_status, artifact.created_at,
			artifact.updated_at, artifact.expires_at, artifact.deleted_at, artifact.failure_code,
			artifact.lease_expires_at, artifact.delete_attempts`,
		now, limit, owner, now.Add(duration))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ArtifactDeletionLease, 0)
	for rows.Next() {
		var object ArtifactObject
		var expiresAt time.Time
		var attempt uint32
		if err := scanArtifact(rows, &object, &expiresAt, &attempt); err != nil {
			return nil, err
		}
		result = append(result, ArtifactDeletionLease{Object: object, Owner: owner, ExpiresAt: expiresAt, Attempt: attempt})
	}
	return result, rows.Err()
}

func (s *PostgresStore) RenewArtifactLease(
	ctx context.Context,
	lease ArtifactDeletionLease,
	now time.Time,
	duration time.Duration,
) (ArtifactDeletionLease, error) {
	expiresAt := now.Add(duration)
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_artifacts SET lease_expires_at=$1
		WHERE tenant_id=$2 AND id=$3 AND status='DELETING' AND lease_owner=$4 AND lease_expires_at>$5`,
		expiresAt, lease.Object.TenantID, lease.Object.ID, lease.Owner, now)
	if err != nil {
		return ArtifactDeletionLease{}, err
	}
	if command.RowsAffected() != 1 {
		return ArtifactDeletionLease{}, ErrLeaseLost
	}
	lease.ExpiresAt = expiresAt
	return lease, nil
}

func (s *PostgresStore) CompleteArtifactDeletion(
	ctx context.Context,
	lease ArtifactDeletionLease,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	object, found, err := getArtifactRow(ctx, tx, lease.Object.TenantID, lease.Object.ID, true)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	command, err := tx.Exec(ctx, `
		UPDATE afh_artifacts SET status='DELETED', updated_at=$1, deleted_at=$1,
			lease_owner='', lease_expires_at=NULL
		WHERE tenant_id=$2 AND id=$3 AND status='DELETING' AND lease_owner=$4`,
		now, object.TenantID, object.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	command, err = tx.Exec(ctx, `
		UPDATE afh_artifact_usage SET bytes=bytes-$1, objects=objects-1
		WHERE tenant_id=$2 AND bytes >= $1 AND objects > 0`, object.SizeBytes, object.TenantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("artifact usage invariant violated")
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) RetryArtifactDeletion(
	ctx context.Context,
	lease ArtifactDeletionLease,
	availableAt time.Time,
) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE afh_artifacts SET available_at=$1, lease_owner='', lease_expires_at=NULL
		WHERE tenant_id=$2 AND id=$3 AND status='DELETING' AND lease_owner=$4`,
		availableAt, lease.Object.TenantID, lease.Object.ID, lease.Owner)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

type artifactRow interface {
	Scan(...any) error
}

type artifactQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getArtifactRow(
	ctx context.Context,
	query artifactQuery,
	tenantID, id string,
	forUpdate bool,
) (ArtifactObject, bool, error) {
	statement := `
		SELECT tenant_id, id, task_id, artifact_id, part_index, storage_key, sha256, size_bytes,
			declared_media_type, detected_media_type, filename, source_uri, status, scan_status,
			created_at, updated_at, expires_at, deleted_at, failure_code
		FROM afh_artifacts WHERE tenant_id=$1 AND id=$2`
	if forUpdate {
		statement += " FOR UPDATE"
	}
	var object ArtifactObject
	err := scanArtifact(query.QueryRow(ctx, statement, tenantID, id), &object)
	if errors.Is(err, pgx.ErrNoRows) {
		return ArtifactObject{}, false, nil
	}
	return object, err == nil, err
}

func scanArtifact(row artifactRow, object *ArtifactObject, extra ...any) error {
	targets := []any{
		&object.TenantID, &object.ID, &object.TaskID, &object.ArtifactID, &object.PartIndex,
		&object.StorageKey, &object.SHA256, &object.SizeBytes, &object.DeclaredMediaType,
		&object.DetectedMediaType, &object.Filename, &object.SourceURI, &object.Status,
		&object.ScanStatus, &object.CreatedAt, &object.UpdatedAt, &object.ExpiresAt,
		&object.DeletedAt, &object.FailureCode,
	}
	targets = append(targets, extra...)
	return row.Scan(targets...)
}

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

func (s *PostgresStore) Health(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("PostgreSQL store is not initialized")
	}
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return nil
}

type taskRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanTasks(rows taskRows) ([]Task, error) {
	result := make([]Task, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var task Task
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, fmt.Errorf("decode stored Task: %w", err)
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func encodeTaskAndEvent(task Task, event Event) ([]byte, []byte, error) {
	taskPayload, err := json.Marshal(task)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Task: %w", err)
	}
	eventPayload, err := json.Marshal(event)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Event: %w", err)
	}
	return taskPayload, eventPayload, nil
}

func mapPostgresNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
