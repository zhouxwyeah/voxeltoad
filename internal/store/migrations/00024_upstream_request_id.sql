-- +goose Up
-- +goose StatementBegin

-- Per-request upstream correlation: the request ID returned by the upstream
-- provider in its response (OpenAI's x-request-id header, Anthropic's
-- request-id header / body request_id, etc.). Stored on request_logs so the
-- gateway can map a gateway request to the provider-side request for support,
-- reconciliation, and incident follow-up. Captured only for the final/successful
-- attempt; per-attempt upstream IDs (including failed retries/failovers) are a
-- separate follow-up (see docs/ops/failover-troubleshooting.md §8).
--
-- Unlike request_id (which may be client-supplied and reused within a session),
-- upstream_request_id is provider-assigned and unique per upstream HTTP call.
--
-- request_logs is RANGE-partitioned by created_at; ADD COLUMN and CREATE INDEX
-- both propagate to all partitions automatically (same pattern as 00010/00017).

ALTER TABLE request_logs ADD COLUMN upstream_request_id TEXT NOT NULL DEFAULT '';

-- Supports reverse lookups: "which gateway request does this upstream req_xxx
-- belong to?" — the primary support/reconciliation access path.
CREATE INDEX idx_request_logs_upstream_request_id ON request_logs (upstream_request_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_request_logs_upstream_request_id;
ALTER TABLE request_logs DROP COLUMN IF EXISTS upstream_request_id;

-- +goose StatementEnd
