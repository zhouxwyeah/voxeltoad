-- +goose Up
-- +goose StatementBegin

-- Per-request audit tracing: every request_logs row carries a unique gateway-
-- assigned request_id (ADR-0021 §5) and an optional session_id from the client
-- (X-Voxeltoad-Session header). Together they enable end-to-end request
-- correlation and session-scoped analysis (Phase-2 tracing story).

ALTER TABLE request_logs ADD COLUMN request_id VARCHAR NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN session_id VARCHAR NOT NULL DEFAULT '';

-- Index backing session-scoped queries (e.g., "show all requests in a session"
-- for debugging and cost-attribution UIs). Combined with created_at to keep
-- time-range scans efficient even for long-lived session keys.
CREATE INDEX idx_request_logs_session_created ON request_logs (session_id, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_request_logs_session_created;
ALTER TABLE request_logs DROP COLUMN IF EXISTS session_id;
ALTER TABLE request_logs DROP COLUMN IF EXISTS request_id;

-- +goose StatementEnd
