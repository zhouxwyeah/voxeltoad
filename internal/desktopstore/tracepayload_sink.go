package desktopstore

import (
	"context"

	"voxeltoad/internal/observability"
)

// TracePayloadSink is the desktop SQLite implementation of
// observability.TracePayloadSink. It captures the message + raw layers
// (ADR-0039): messages and request_raw are stored as JSON text; response_raw
// and error_raw are verbatim text (streaming responses are SSE transcripts, not
// JSON). JSONB on PostgreSQL maps to TEXT here.
type TracePayloadSink struct {
	db *DB
}

// NewTracePayloadSink builds a SQLite-backed observability.TracePayloadSink.
func NewTracePayloadSink(db *DB) *TracePayloadSink { return &TracePayloadSink{db: db} }

// Record implements observability.TracePayloadSink.
func (s *TracePayloadSink) Record(ctx context.Context, p observability.TracePayload) error {
	row := TracePayloadRow{
		RequestID:      p.RequestID,
		SessionID:      p.SessionID,
		TraceID:        p.TraceID,
		Tenant:         p.Tenant,
		Group:          p.Group,
		APIKeyID:       p.APIKeyID,
		Provider:       p.Provider,
		ModelRequested: p.ModelRequested,
		Stream:         p.Stream,
		AgentType:      p.AgentType,
		StatusCode:     p.StatusCode,
		StopReason:     p.StopReason,
		NMessages:      p.NMessages,
		NToolUse:       p.NToolUse,
		Messages:       string(p.Messages),
		RequestRaw:     string(p.RequestRaw),
		ResponseRaw:    p.ResponseRaw,
		ErrorRaw:       p.ErrorRaw,
		CreatedAt:      p.CreatedAt,
	}
	return s.db.WithContext(ctx).Create(&row).Error
}
