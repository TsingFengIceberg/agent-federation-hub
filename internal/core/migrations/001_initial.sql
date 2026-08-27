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

CREATE TABLE IF NOT EXISTS afh_token_revocations (
    issuer TEXT NOT NULL,
    token_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    revoked_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (issuer, token_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS afh_token_revocations_expiry_idx
    ON afh_token_revocations (expires_at);

CREATE TABLE IF NOT EXISTS afh_artifact_usage (
    tenant_id TEXT PRIMARY KEY,
    bytes BIGINT NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    objects BIGINT NOT NULL DEFAULT 0 CHECK (objects >= 0)
);

CREATE TABLE IF NOT EXISTS afh_artifacts (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    part_index INTEGER NOT NULL,
    storage_key TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    declared_media_type TEXT NOT NULL DEFAULT '',
    detected_media_type TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    source_uri TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    scan_status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    failure_code TEXT NOT NULL DEFAULT '',
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    delete_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, task_id) REFERENCES afh_tasks (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS afh_artifacts_expiry_idx
    ON afh_artifacts (available_at, lease_expires_at, expires_at)
    WHERE status IN ('AVAILABLE', 'QUARANTINED', 'DELETING');
