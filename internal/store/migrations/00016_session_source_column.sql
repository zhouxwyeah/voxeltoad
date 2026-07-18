-- +goose Up
-- +goose StatementBegin

-- Per-request session-source label: which mechanism supplied the session key used
-- for affinity routing (header-config / header-generic / body-session /
-- body-metadata / body-user / prefix). Recorded on request_logs only — it is a
-- routing-affinity observability field, unrelated to cost, so usage_records is
-- left untouched. Mirrors the trace_id column style from 00015. The table is
-- RANGE-partitioned by created_at; ADD COLUMN propagates to all partitions
-- automatically.

ALTER TABLE request_logs ADD COLUMN session_source VARCHAR NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE request_logs DROP COLUMN IF EXISTS session_source;

-- +goose StatementEnd
