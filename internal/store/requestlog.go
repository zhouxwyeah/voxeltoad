package store

import (
	"context"

	"voxeltoad/internal/observability"
)

// RequestLogRepo is the PostgreSQL implementation of
// observability.RequestLogSink: the durable backend the async request-audit
// recorder flushes to. Off the request hot path, so a plain gorm insert is
// fine. Identities are denormalized strings (tenant/group names, public
// api_key_id), matching usage_records (ADR-0014).
type RequestLogRepo struct {
	db *DB
}

// NewRequestLogRepo builds a RequestLogRepo over the given connection.
func NewRequestLogRepo(db *DB) *RequestLogRepo { return &RequestLogRepo{db: db} }

// Record inserts a single request-audit row. created_at defaults to now() when
// the record carries the zero time.
func (r *RequestLogRepo) Record(ctx context.Context, rec observability.RequestLog) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO request_logs
		   (tenant, group_name, api_key_id, provider,
		    model_requested, model_resolved, stream,
		    prompt_tokens, completion_tokens, total_tokens,
		    ttft_ms, duration_ms, error_type, blocked_by, fallback,
		    request_id, session_id, trace_id, session_source, agent_type,
		    cache_hit, cache_tier, cache_source, cached_prompt_tokens,
		    upstream_request_id, ingress_protocol)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Tenant, rec.Group, rec.APIKeyID, rec.Provider,
		rec.ModelRequested, rec.ModelResolved, rec.Stream,
		rec.PromptTokens, rec.CompletionTokens, rec.TotalTokens,
		rec.TTFTms, rec.Durationms, rec.ErrorType, rec.BlockedBy, rec.Fallback,
		rec.RequestID, rec.SessionID, rec.TraceID, rec.SessionSource, rec.AgentType,
		rec.CacheHit, rec.CacheTier, rec.CacheSource, rec.CachedPromptTokens,
		rec.UpstreamRequestID, rec.IngressProtocol,
	).Error
}
