-- +goose Up
-- +goose StatementBegin

-- Per-request ingress protocol label: which client wire protocol served the
-- request ("openai" for /v1/chat/completions, "anthropic" for /v1/messages).
-- "" for pre-migration rows (the gateway served OpenAI-only before ADR-0045).
-- Added to both ledgers so the management UI can filter/group by protocol
-- without a join — same pattern as agent_type (migration 00023):
--   - request_logs: drives the per-request list protocol filter + the
--     passthrough/translated badge (compared against the hit provider's
--     adapter) used by operators to verify dual-provider routing.
--   - trace_payloads: surfaced as a summary dimension so the request-list /
--     session-timeline views render it without decoding the JSONB bodies.
--
-- See ADR-0046 for why this is persisted (unlike RetryCount which stays
-- telemetry-only): ingress_protocol is an operator filter/attribution
-- dimension (agent_type's class), not a diagnostic counter (RetryCount's).
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN propagates to all
-- partitions automatically. Mirrors agent_type (00023) and session_source (00016).

ALTER TABLE request_logs   ADD COLUMN ingress_protocol VARCHAR NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN ingress_protocol VARCHAR NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE request_logs   DROP COLUMN IF EXISTS ingress_protocol;
ALTER TABLE trace_payloads DROP COLUMN IF EXISTS ingress_protocol;

-- +goose StatementEnd
