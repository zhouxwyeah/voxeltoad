package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// TracePayloadQueryRepo is the read side of trace_payloads for the management
// plane (ADR-0039), following the same read-API pattern as RequestLogQueryRepo
// (ADR-0021 §7 / ADR-0019). READ-ONLY (writes go through TracePayloadRepo on the
// data plane). Tenant is bound at construction: a non-empty tenant scopes every
// query to that tenant's rows (structural isolation for tenant-admin reads); an
// empty tenant is the global view (super-admin).
//
// Because trace_payloads carries prompt/completion plaintext, READS are more
// sensitive than request_logs: the detail read (GetByRequestID) is intended to be
// audited at the handler layer (ADR-0039 §5).
type TracePayloadQueryRepo struct {
	db     *DB
	tenant string // "" = global (super-admin)
}

// NewTracePayloadQueryRepo builds a read repo. tenant=="" means global.
func NewTracePayloadQueryRepo(db *DB, tenant string) *TracePayloadQueryRepo {
	return &TracePayloadQueryRepo{db: db, tenant: tenant}
}

// TracePayloadSummaryRow is one trace event as shown in a "request list" view —
// the summary dimensions only, WITHOUT the large JSONB bodies. This lets the
// session/request list render rows cheaply; the bodies are fetched on demand via
// GetByRequestID when an operator drills into a single request.
type TracePayloadSummaryRow struct {
	ID             int64     `json:"id"`
	RequestID      string    `json:"request_id"`
	SessionID      string    `json:"session_id"`
	TraceID        string    `json:"trace_id"`
	Tenant         string    `json:"tenant"`
	Provider       string    `json:"provider"`
	ModelRequested string    `json:"model_requested"`
	Stream         bool      `json:"stream"`
	AgentType      string    `json:"agent_type"`
	StatusCode     int       `json:"status_code"`
	StopReason     string    `json:"stop_reason"`
	NMessages      int       `json:"n_messages"`
	NToolUse       int       `json:"n_tool_use"`
	CreatedAt      time.Time `json:"created_at"`
}

// TracePayloadDetail is the full payload for a single request: the message +
// raw layers. messages/request_raw are JSONB and returned as json.RawMessage so
// the API can emit them verbatim without a Go round-trip. response_raw is TEXT
// (the verbatim upstream body, which may be an SSE transcript) and returned as a
// string.
type TracePayloadDetail struct {
	TracePayloadSummaryRow
	Messages    json.RawMessage `json:"messages"`
	RequestRaw  json.RawMessage `json:"request_raw"`
	ResponseRaw string          `json:"response_raw"`
	ErrorRaw    string          `json:"error_raw"`
}

// summaryCols lists the columns fetched for a summary row (no JSONB), shared by
// ListBySession and the summary projection of GetByRequestID.
const traceSummaryCols = `id, request_id, session_id, trace_id, tenant,
       provider, model_requested, stream, agent_type,
       status_code, stop_reason, n_messages, n_tool_use, created_at`

// ListBySession returns the trace events for a session_id in chronological ASC
// order (oldest first) — the timeline order a session-trace view needs. Summary
// only: no JSONB bodies. The bound tenant scope applies (a tenant-admin sees
// only its own rows). session_id must be non-empty. Hits
// idx_trace_payloads_session_created. Capped at limit to bound cost.
func (r *TracePayloadQueryRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]TracePayloadSummaryRow, error) {
	if sessionID == "" {
		return []TracePayloadSummaryRow{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	where, args := r.buildScope(sessionID)
	args = append(args, limit)
	q := `SELECT ` + traceSummaryCols + `
	      FROM trace_payloads
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY created_at ASC, id ASC
	      LIMIT ?`

	var rows []TracePayloadSummaryRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []TracePayloadSummaryRow{}
	}
	return rows, nil
}

// GetByRequestID returns the full payload for a single request (summary +
// messages + raw bodies), or ok=false when no row matches. The bound tenant
// scope applies. Intended to be audited at the handler layer (ADR-0039 §5).
func (r *TracePayloadQueryRepo) GetByRequestID(ctx context.Context, requestID string) (TracePayloadDetail, bool, error) {
	var d TracePayloadDetail
	if requestID == "" {
		return d, false, nil
	}
	where, args := r.buildScopeForRequest(requestID)
	q := `SELECT ` + traceSummaryCols + `, messages, request_raw, response_raw, error_raw
	      FROM trace_payloads
	      WHERE ` + strings.Join(where, " AND ") + `
	      LIMIT 1`
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&d).Error; err != nil {
		return d, false, err
	}
	return d, d.RequestID != "", nil
}

// GetByRowID returns the full payload for a single row by its primary key id.
// The bound tenant scope applies. This is the preferred lookup when a client
// may send duplicate X-Request-Id headers across requests in a session: unlike
// GetByRequestID (which uses LIMIT 1 and thus always returns the same row when
// request_id is duplicated), GetByRowID addresses each row uniquely via the
// table's identity primary key.
func (r *TracePayloadQueryRepo) GetByRowID(ctx context.Context, rowID int64) (TracePayloadDetail, bool, error) {
	var d TracePayloadDetail
	if rowID <= 0 {
		return d, false, nil
	}
	where := []string{"id = ?"}
	args := []any{rowID}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	q := `SELECT ` + traceSummaryCols + `, messages, request_raw, response_raw, error_raw
	      FROM trace_payloads
	      WHERE ` + strings.Join(where, " AND ") + `
	      LIMIT 1`
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&d).Error; err != nil {
		return d, false, err
	}
	return d, d.ID != 0, nil
}

// buildScope assembles the WHERE clauses for a session-scoped query. The bound
// tenant is always applied (structural isolation); session_id is required.
func (r *TracePayloadQueryRepo) buildScope(sessionID string) (where []string, args []any) {
	where = []string{"1=1"}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	where = append(where, "session_id = ?")
	args = append(args, sessionID)
	return where, args
}

// buildScopeForRequest assembles the WHERE clauses for a request-id lookup. The
// bound tenant is always applied; request_id is required.
func (r *TracePayloadQueryRepo) buildScopeForRequest(requestID string) (where []string, args []any) {
	where = []string{"1=1"}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	where = append(where, "request_id = ?")
	args = append(args, requestID)
	return where, args
}
