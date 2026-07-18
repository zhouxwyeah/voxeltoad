package desktopstore

import (
	"context"

	"voxeltoad/internal/observability"
)

// RequestLogSink is the desktop SQLite implementation of
// observability.RequestLogSink. It maps the provider-agnostic RequestLog
// (metadata only — no bodies) onto the request_logs table.
type RequestLogSink struct {
	db *DB
}

// NewRequestLogSink builds a SQLite-backed observability.RequestLogSink.
func NewRequestLogSink(db *DB) *RequestLogSink { return &RequestLogSink{db: db} }

// Record implements observability.RequestLogSink.
func (s *RequestLogSink) Record(ctx context.Context, r observability.RequestLog) error {
	row := RequestLogRow{
		Tenant:             r.Tenant,
		Group:              r.Group,
		APIKeyID:           r.APIKeyID,
		Provider:           r.Provider,
		ModelRequested:     r.ModelRequested,
		ModelResolved:      r.ModelResolved,
		Stream:             r.Stream,
		PromptTokens:       r.PromptTokens,
		CompletionTokens:   r.CompletionTokens,
		TotalTokens:        r.TotalTokens,
		TTFTms:             r.TTFTms,
		Durationms:         r.Durationms,
		ErrorType:          r.ErrorType,
		BlockedBy:          r.BlockedBy,
		Fallback:           r.Fallback,
		CacheHit:           r.CacheHit,
		CachedPromptTokens: r.CachedPromptTokens,
		CacheTier:          r.CacheTier,
		CacheSource:        r.CacheSource,
		RequestID:          r.RequestID,
		SessionID:          r.SessionID,
		TraceID:            r.TraceID,
		UpstreamRequestID:  r.UpstreamRequestID,
		SessionSource:      r.SessionSource,
		AgentType:          r.AgentType,
		CreatedAt:          r.CreatedAt,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}
