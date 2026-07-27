-- +goose Up
-- +goose StatementBegin

-- Per-request raw User-Agent header value (verbatim, after trim, capped to a
-- sane length in the data plane). Added alongside agent_type so the original
-- client UA string is recoverable for agent-detection rule tuning and
-- diagnostics: agent_type is a derived label ("claude-code", ...) computed by
-- substring-matching the UA, which discards the source string. With the raw UA
-- persisted, an unrecognized client (e.g. a new agent SDK) can be identified
-- from request_logs directly instead of re-capturing it. Mirrors the
-- agent_type column style from 00023.
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN propagates to all
-- partitions automatically. "" for pre-existing rows (no backfill). NOT NULL
-- DEFAULT '' keeps the column cheap and uniform with the other id/label columns.

ALTER TABLE request_logs  ADD COLUMN user_agent VARCHAR NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN user_agent VARCHAR NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE request_logs  DROP COLUMN IF EXISTS user_agent;
ALTER TABLE trace_payloads DROP COLUMN IF EXISTS user_agent;

-- +goose StatementEnd
