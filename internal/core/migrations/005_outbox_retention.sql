ALTER TABLE afh_outbox
    ADD COLUMN IF NOT EXISTS purged_at TIMESTAMPTZ;

DROP INDEX IF EXISTS afh_outbox_ready_idx;

CREATE INDEX IF NOT EXISTS afh_outbox_ready_idx
    ON afh_outbox (available_at, lease_expires_at, created_at)
    WHERE acked_at IS NULL AND dead_lettered_at IS NULL AND purged_at IS NULL;
