ALTER TABLE afh_outbox
    ADD COLUMN IF NOT EXISTS dead_lettered_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS afh_outbox_ready_idx
    ON afh_outbox (available_at, lease_expires_at, created_at)
    WHERE acked_at IS NULL AND dead_lettered_at IS NULL;
