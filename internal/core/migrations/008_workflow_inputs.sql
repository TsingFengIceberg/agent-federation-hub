CREATE TABLE IF NOT EXISTS afh_workflow_inputs (
    tenant_id TEXT NOT NULL,
    reference TEXT NOT NULL,
    payload BYTEA NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, reference)
);

CREATE INDEX IF NOT EXISTS afh_workflow_inputs_tenant_created_idx
    ON afh_workflow_inputs (tenant_id, created_at);
