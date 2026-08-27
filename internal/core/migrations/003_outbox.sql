CREATE TABLE IF NOT EXISTS afh_outbox (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    dedup_key TEXT NOT NULL,
    topic TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    attempts INTEGER NOT NULL DEFAULT 0,
    acked_at TIMESTAMPTZ,
    UNIQUE (tenant_id, task_id, dedup_key),
    FOREIGN KEY (tenant_id, task_id) REFERENCES afh_tasks (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS afh_outbox_pending_idx
    ON afh_outbox (available_at, lease_expires_at, created_at)
    WHERE acked_at IS NULL;
