CREATE TABLE IF NOT EXISTS afh_workflows (
    tenant_id TEXT NOT NULL,
    id TEXT NOT NULL,
    payload JSONB NOT NULL,
    revision BIGINT NOT NULL,
    state TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS afh_workflows_tenant_updated_idx
    ON afh_workflows (tenant_id, updated_at, id);

CREATE TABLE IF NOT EXISTS afh_workflow_events (
    tenant_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    dedup_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, workflow_id, sequence),
    UNIQUE (tenant_id, workflow_id, dedup_key),
    FOREIGN KEY (tenant_id, workflow_id) REFERENCES afh_workflows (tenant_id, id) ON DELETE CASCADE
);
