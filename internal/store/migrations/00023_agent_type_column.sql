-- +goose Up
-- +goose StatementBegin

-- Per-request agent/client type label: which agent SDK sent the request
-- (claude-code / codex / codebuddy / workbuddy / opencode / …), detected from
-- the User-Agent header (with x-<vendor>-session-id as a fallback). "" when the
-- client is unrecognized (a plain OpenAI SDK, curl, a browser). Added to both
-- ledgers so the trace UI can filter/group sessions by agent without a join:
--   - request_logs: drives the session-list aggregation (which agent owns each
--     session) and the per-request list.
--   - trace_payloads: surfaced as a summary dimension so the request-list view
--     renders it without decoding the JSONB bodies (mirrors provider/model).
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN propagates to all
-- partitions automatically. Mirrors the session_source column style from 00016.

ALTER TABLE request_logs  ADD COLUMN agent_type VARCHAR NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN agent_type VARCHAR NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE request_logs  DROP COLUMN IF EXISTS agent_type;
ALTER TABLE trace_payloads DROP COLUMN IF EXISTS agent_type;

-- +goose StatementEnd
