CREATE TABLE IF NOT EXISTS afh_worker_controls (
    scope TEXT PRIMARY KEY,
    mode TEXT NOT NULL CHECK (mode IN ('RUNNING', 'PAUSED', 'DRAINING')),
    revision BIGINT NOT NULL CHECK (revision >= 1),
    updated_at TIMESTAMPTZ NOT NULL
);
