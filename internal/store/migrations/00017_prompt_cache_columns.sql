-- +goose Up
-- +goose StatementBegin

-- Upstream prompt-caching hit information (per-(alias, provider) configurable
-- cache_hit_multiplier billing; see internal/billing/cost.go Cost). This
-- migration adds cache columns to both data-plane ledgers so the audit and cost
-- feeds can reconstruct cache behavior.
--
-- request_logs carries the full cache dimensions (hit/tier/source + cached token
-- count) so future admin filters/aggregations have the fields ready; this round
-- intentionally does NOT wire admin query support for them (YAGNI).
-- usage_records carries the cached token count plus the cache discount in
-- micro-units (FullCost - Cost), so operational reports can show "how much did
-- the cache save" without recomputing from token counts and rates.
--
-- Both tables are RANGE-partitioned by created_at; ADD COLUMN propagates to all
-- partitions automatically (same pattern as 00014/00016).

ALTER TABLE request_logs ADD COLUMN cache_hit             BOOLEAN  NOT NULL DEFAULT false;
ALTER TABLE request_logs ADD COLUMN cache_tier            VARCHAR  NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN cache_source          VARCHAR  NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN cached_prompt_tokens  INTEGER  NOT NULL DEFAULT 0;

ALTER TABLE usage_records  ADD COLUMN cached_prompt_tokens  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage_records  ADD COLUMN cache_discount_micros BIGINT  NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE usage_records  DROP COLUMN IF EXISTS cache_discount_micros;
ALTER TABLE usage_records  DROP COLUMN IF EXISTS cached_prompt_tokens;

ALTER TABLE request_logs  DROP COLUMN IF EXISTS cached_prompt_tokens;
ALTER TABLE request_logs  DROP COLUMN IF EXISTS cache_source;
ALTER TABLE request_logs  DROP COLUMN IF EXISTS cache_tier;
ALTER TABLE request_logs  DROP COLUMN IF EXISTS cache_hit;
-- +goose StatementEnd
