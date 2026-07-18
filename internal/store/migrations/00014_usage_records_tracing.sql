-- +goose Up
-- +goose StatementBegin

-- Carry request_id + session_id on usage_records so cost can be correlated to a
-- request session (ADR-0021 §5 tracing). Mirrors the request_logs columns added
-- in migration 00010. On a RANGE-partitioned table, ALTER TABLE ADD COLUMN
-- propagates to all existing partitions automatically.

ALTER TABLE usage_records ADD COLUMN request_id VARCHAR NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN session_id VARCHAR NOT NULL DEFAULT '';

-- Index backing session-scoped cost queries (the session trace view: "what did
-- this session cost?"). Combined with created_at for efficient time-bounded
-- scans.
CREATE INDEX idx_usage_records_session_created ON usage_records (session_id, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_usage_records_session_created;
ALTER TABLE usage_records DROP COLUMN IF EXISTS session_id;
ALTER TABLE usage_records DROP COLUMN IF EXISTS request_id;

-- +goose StatementEnd
