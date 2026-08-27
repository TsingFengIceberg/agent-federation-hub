package core

import (
	"context"
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
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		migration, err := postgresMigrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read PostgreSQL migration %s: %w", entry.Name(), err)
		}
		if _, err := s.pool.Exec(ctx, string(migration)); err != nil {
			return fmt.Errorf("apply PostgreSQL migration %s: %w", entry.Name(), err)
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

func (s *PostgresStore) Close() error {
	s.pool.Close()
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
