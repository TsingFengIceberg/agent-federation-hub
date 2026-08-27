CREATE TABLE IF NOT EXISTS afh_rate_limit_buckets (
    tenant_id TEXT NOT NULL,
    subject TEXT NOT NULL,
    action TEXT NOT NULL,
    tokens DOUBLE PRECISION NOT NULL,
    last_refill TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, subject, action)
);
