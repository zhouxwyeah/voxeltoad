package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"voxeltoad/internal/observability"
)

// TracePayloadRepo is the PostgreSQL implementation of
// observability.TracePayloadSink: the durable backend the async trace-payload
// recorder flushes to (the trace_payloads table, ADR-0039). Off the request hot
// path, so a plain gorm insert is fine. Bodies are passed as json.RawMessage so
// the pgx driver encodes them as JSONB without a re-parse.
type TracePayloadRepo struct {
	db *DB
}

// NewTracePayloadRepo builds a TracePayloadRepo over the given connection.
func NewTracePayloadRepo(db *DB) *TracePayloadRepo { return &TracePayloadRepo{db: db} }

// Record inserts a single trace-payload row. messages/request_raw default to
// '[]'/'{}' in the migration; an empty RawMessage here is normalized to those
// literals so a NULL is never written (the columns are NOT NULL). response_raw
// is TEXT and bound verbatim; an empty string is stored as ”.
func (r *TracePayloadRepo) Record(ctx context.Context, p observability.TracePayload) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO trace_payloads
		   (request_id, session_id, trace_id, tenant, group_name, api_key_id,
		    provider, model_requested, stream, agent_type,
		    status_code, stop_reason, n_messages, n_tool_use,
		    messages, request_raw, response_raw, error_raw)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.RequestID, p.SessionID, p.TraceID, p.Tenant, p.Group, p.APIKeyID,
		p.Provider, p.ModelRequested, p.Stream, p.AgentType,
		p.StatusCode, p.StopReason, p.NMessages, p.NToolUse,
		jsonBody(p.Messages, "[]"), jsonBody(p.RequestRaw, "{}"),
		p.ResponseRaw, p.ErrorRaw,
	).Error
}

// jsonBody normalizes a possibly-empty json.RawMessage to the given empty literal
// so a NOT NULL JSONB column never receives NULL. A non-empty value is returned
// as json.RawMessage (encoded as JSONB by pgx), mirroring store.nullableJSON.
// It is used for messages and request_raw; response_raw is TEXT and bound
// directly.
func jsonBody(b json.RawMessage, empty string) any {
	if len(b) == 0 {
		return json.RawMessage(empty)
	}
	return b
}

// DropTracePayloadPartitionsBefore drops every monthly trace_payloads partition
// whose entire range is before cutoff. This is the partition-DROP TTL for the
// short-retention trace ledger (ADR-0039 §4): one DROP per expired month is O(1)
// and avoids the bloat of a DELETE scan over large JSONB rows.
//
// A partition trace_payloads_YYYY_MM covers [month start, next month start). It is
// only dropped when cutoff has passed into a LATER month than the partition's
// month (strict <), so the current month is never dropped even when cutoff falls
// inside it — this preserves rows still within the retention window.
//
// Only named monthly partitions (trace_payloads_YYYY_MM) are dropped; the
// DEFAULT partition is never touched (it may hold out-of-window rows that are
// not worth the risk of a blanket drop). Returns the number of partitions
// dropped. Safe to run repeatedly: a no-op when nothing is expired.
func (r *TracePayloadRepo) DropTracePayloadPartitionsBefore(ctx context.Context, cutoff time.Time) (int, error) {
	const q = `
		WITH expired AS (
			SELECT c.relname AS child
			FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid
			JOIN pg_class p ON p.oid = i.inhparent
			JOIN pg_namespace n ON n.oid = p.relnamespace
			JOIN pg_class dc ON dc.oid = i.inhrelid
			WHERE n.nspname = 'public'
			  AND p.relname = 'trace_payloads'
			  AND dc.relname LIKE 'trace_payloads________%'
			  AND dc.relname <> 'trace_payloads_default'
			)
		SELECT child FROM expired
		WHERE (regexp_match(child, 'trace_payloads_(\d{4})_(\d{2})'))[1]::int * 100
		    + (regexp_match(child, 'trace_payloads_(\d{4})_(\d{2})'))[2]::int
		  < to_char(date_trunc('month', $1::timestamptz), 'YYYYMM')::int`
	var expired []string
	if err := r.db.WithContext(ctx).Raw(q, cutoff).Scan(&expired).Error; err != nil {
		return 0, err
	}
	for _, name := range expired {
		// relname came from pg_class and matches our strict LIKE pattern, so it is
		// a known-shape identifier; still, format(%I) quotes it defensively.
		if err := r.db.WithContext(ctx).Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q CASCADE`, name)).Error; err != nil {
			return 0, err
		}
	}
	return len(expired), nil
}
