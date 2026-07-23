package store

import (
	"context"

	"gorm.io/gorm"

	"voxeltoad/internal/billing"
)

// UsageRepo is the PostgreSQL implementation of billing.UsageSink (ADR-0016):
// the durable backend the async usage recorder flushes to. Off the money hot
// path (quota already settled), so plain gorm inserts are fine. Identities are
// denormalized strings (tenant/group names, public api_key_id) — see ADR-0014.
type UsageRepo struct {
	db *DB
}

// NewUsageRepo builds a UsageRepo over the given connection.
func NewUsageRepo(db *DB) *UsageRepo { return &UsageRepo{db: db} }

// Record inserts a single usage record.
func (r *UsageRepo) Record(ctx context.Context, rec billing.UsageRecord) error {
	return r.RecordBatch(ctx, []billing.UsageRecord{rec})
}

// RecordBatch inserts a batch of usage records in one statement (the async
// recorder flushes in batches).
func (r *UsageRepo) RecordBatch(ctx context.Context, recs []billing.UsageRecord) error {
	if len(recs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, rec := range recs {
			if err := tx.Exec(
				`INSERT INTO usage_records
				   (tenant, group_name, api_key_id, provider, provider_endpoint, model,
				    prompt_tokens, completion_tokens, cost,
				    request_id, session_id, trace_id,
				    cached_prompt_tokens, cache_discount_micros)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				rec.Tenant, rec.Group, rec.APIKeyID, rec.Provider, rec.ProviderEndpoint, rec.Model,
				rec.PromptTokens, rec.CompletionTokens, rec.Cost,
				rec.RequestID, rec.SessionID, rec.TraceID,
				rec.CachedPromptTokens, rec.CacheDiscountMicros,
			).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
