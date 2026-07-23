-- +goose Up
-- +goose StatementBegin

-- Client-supplied request id (ADR-0050). The gateway ALWAYS generates its own
-- request_id (unique per request); the client's original X-Request-Id header
-- value is preserved here verbatim (after trim) for cross-system correlation.
-- Empty when the client did not send the header.
--
-- Why split: some agent clients (Claude Code, Codex, …) reuse the same
-- X-Request-Id across every request in a session. Adopting that value as
-- request_id produced duplicate rows in request_logs, breaking per-request
-- correlation, LIMIT 1 lookups, and UI lists. Regenerating gateway-side
-- guarantees uniqueness; the original survives in client_request_id.
--
-- Added to BOTH ledgers so the management UI can render it without a join:
--   - request_logs: drives the client-request-id filter + the correlation
--     debug column when tracing a client-side id to a gateway request.
--   - trace_payloads: surfaced so the trace detail view can display the
--     three-id tuple (gateway / client / upstream) without a join.
--
-- See ADR-0050 for the full decision context. This migration revises the
-- request_id semantics recorded in ADR-0021 §5.
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN and CREATE INDEX
-- both propagate to all partitions automatically (same pattern as 00010/00017/
-- 00024). No backfill: pre-migration rows keep client_request_id = ''.

ALTER TABLE request_logs   ADD COLUMN client_request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN client_request_id TEXT NOT NULL DEFAULT '';

-- Supports reverse lookups: "which gateway request carried client X-Request-Id
-- = abc-123?" — useful when a client reports an id from its own logs and
-- support needs to find the gateway-side request. Same access pattern as
-- upstream_request_id (00024).
CREATE INDEX idx_request_logs_client_request_id   ON request_logs (client_request_id);
CREATE INDEX idx_trace_payloads_client_request_id ON trace_payloads (client_request_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_trace_payloads_client_request_id;
DROP INDEX IF EXISTS idx_request_logs_client_request_id;
ALTER TABLE trace_payloads DROP COLUMN IF EXISTS client_request_id;
ALTER TABLE request_logs   DROP COLUMN IF EXISTS client_request_id;

-- +goose StatementEnd
