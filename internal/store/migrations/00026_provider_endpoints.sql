-- +goose Up
-- +goose StatementBegin

-- Multi-endpoint Provider (ADR-0049): a provider can carry several
-- (adapter, base_url) endpoints (e.g. a dual-protocol vendor with both an
-- OpenAI-compatible and an Anthropic-compatible endpoint). The runtime picks
-- the endpoint whose adapter matches the ingress protocol, so the audit
-- ledgers must record which endpoint actually served the request — otherwise
-- cost/usage attribution is ambiguous (was this the provider's openai endpoint
-- or its anthropic one?).
--
-- provider_endpoint is the endpoint's stable slug (ProviderEndpoint.ID, or the
-- adapter-derived default: openai / anthropic). Empty for rows written before
-- this migration and for single-provider test harnesses. Both ledgers mirror
-- the same column set as ingress_protocol (00025).
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN propagates to
-- all partitions automatically. Indexes support per-(provider, endpoint)
-- drill-down in the usage/audit views.

ALTER TABLE request_logs   ADD COLUMN provider_endpoint VARCHAR NOT NULL DEFAULT '';
ALTER TABLE usage_records  ADD COLUMN provider_endpoint VARCHAR NOT NULL DEFAULT '';
ALTER TABLE trace_payloads ADD COLUMN provider_endpoint VARCHAR NOT NULL DEFAULT '';

CREATE INDEX idx_request_logs_provider_endpoint  ON request_logs   (provider, provider_endpoint, created_at);
CREATE INDEX idx_usage_records_provider_endpoint ON usage_records  (provider, provider_endpoint, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_request_logs_provider_endpoint;
DROP INDEX IF EXISTS idx_usage_records_provider_endpoint;

ALTER TABLE request_logs   DROP COLUMN IF EXISTS provider_endpoint;
ALTER TABLE usage_records  DROP COLUMN IF EXISTS provider_endpoint;
ALTER TABLE trace_payloads DROP COLUMN IF EXISTS provider_endpoint;

-- +goose StatementEnd
