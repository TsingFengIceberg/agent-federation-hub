CREATE TABLE IF NOT EXISTS afh_agents (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS afh_tasks (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    payload JSONB NOT NULL,
    revision BIGINT NOT NULL,
    state TEXT NOT NULL,
    remote_task_id TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    reconcile_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS afh_tasks_recoverable_idx
    ON afh_tasks (available_at, lease_expires_at, updated_at)
    WHERE remote_task_id <> '' AND state NOT IN ('COMPLETED', 'FAILED', 'CANCELED', 'REJECTED');

CREATE TABLE IF NOT EXISTS afh_events (
    tenant_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    dedup_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, task_id, sequence),
    UNIQUE (tenant_id, task_id, dedup_key),
    FOREIGN KEY (tenant_id, task_id) REFERENCES afh_tasks (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS afh_inbox (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    protocol TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    attempts INTEGER NOT NULL DEFAULT 0,
    acked_at TIMESTAMPTZ,
    UNIQUE (tenant_id, task_id, dedup_key),
    FOREIGN KEY (tenant_id, task_id) REFERENCES afh_tasks (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS afh_inbox_pending_idx
    ON afh_inbox (available_at, lease_expires_at, created_at)
    WHERE acked_at IS NULL;
