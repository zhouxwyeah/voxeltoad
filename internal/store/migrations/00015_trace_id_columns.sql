-- +goose Up
-- +goose StatementBegin

-- Per-request W3C trace id: the 32-hex trace segment parsed from an incoming
-- traceparent header. Carried on request_logs (audit) and usage_records (cost)
-- so gateway rows join to upstream distributed traces by trace id, completing the
-- request → session → trace correlation story (ADR-0021 §5). Mirrors the
-- request_id/session_id columns added in 00010/00014. Both tables are
-- RANGE-partitioned by created_at; ADD COLUMN propagates to all partitions
-- automatically, so no per-partition DDL is needed.

ALTER TABLE request_logs   ADD COLUMN trace_id VARCHAR NOT NULL DEFAULT '';
ALTER TABLE usage_records  ADD COLUMN trace_id VARCHAR NOT NULL DEFAULT '';

-- Index backing exact trace-id lookups (debug: "show every request in this
-- distributed trace"). Created independently of the session index because a
-- trace can span many sessions.
CREATE INDEX idx_request_logs_trace_id      ON request_logs   (trace_id);
CREATE INDEX idx_usage_records_trace_id     ON usage_records  (trace_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_usage_records_trace_id;
DROP INDEX IF EXISTS idx_request_logs_trace_id;
ALTER TABLE usage_records  DROP COLUMN IF EXISTS trace_id;
ALTER TABLE request_logs   DROP COLUMN IF EXISTS trace_id;

-- +goose StatementEnd
