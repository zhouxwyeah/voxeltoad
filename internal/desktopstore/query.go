// Package store implements the desktop gateway's SQLite-backed persistence.
//
// query.go is the READ side used by the desktop UI (design/desktop.md §10.2):
// request-log listing, session aggregation, trace payload detail, and a
// per-agent overview rollup. It mirrors the query *semantics* of the enterprise
// repos (internal/store/requestlog_query.go, tracepayload_query.go) but is
// rewritten in SQLite-compatible SQL — SQLite has no array_agg(...) FILTER or
// bool_or, so per-session "latest agent type" uses a correlated subquery and
// booleans use MAX(CASE ...). The desktop gateway is single-tenant (the seeded
// default key), so unlike the enterprise repos there is no tenant scope to bind.
package desktopstore

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// QueryRepo is the read side of request_logs + trace_payloads for the desktop
// UI. It is READ-ONLY; writes go through the Async*Recorder sinks.
type QueryRepo struct {
	db *DB
}

// NewQueryRepo builds a read repo over the given SQLite connection.
func NewQueryRepo(db *DB) *QueryRepo { return &QueryRepo{db: db} }

// ---------------------------------------------------------------------------
// request_logs
// ---------------------------------------------------------------------------

// RequestLogFilter narrows a request-log query. Empty fields are ignored. The
// desktop gateway is single-tenant, so there is no tenant/key drill-down.
type RequestLogFilter struct {
	Provider       string
	ModelRequested string
	ErrorType      string
	SessionID      string
	RequestID      string
	AgentType      string
	From, To       time.Time
}

// RequestLogView is one request-audit entry as returned to the UI, mirroring
// the request_logs columns with the same JSON shape the enterprise admin API
// emits (so the reused UI components need no remapping).
type RequestLogView struct {
	ID                 int64     `gorm:"column:id" json:"id"`
	Tenant             string    `gorm:"column:tenant" json:"tenant"`
	GroupName          string    `gorm:"column:group_name" json:"group_name"`
	APIKeyID           string    `gorm:"column:api_key_id" json:"api_key_id"`
	Provider           string    `gorm:"column:provider" json:"provider"`
	ModelRequested     string    `gorm:"column:model_requested" json:"model_requested"`
	ModelResolved      string    `gorm:"column:model_resolved" json:"model_resolved"`
	Stream             bool      `gorm:"column:stream" json:"stream"`
	PromptTokens       int       `gorm:"column:prompt_tokens" json:"prompt_tokens"`
	CompletionTokens   int       `gorm:"column:completion_tokens" json:"completion_tokens"`
	TotalTokens        int       `gorm:"column:total_tokens" json:"total_tokens"`
	TTFTms             int       `gorm:"column:ttft_ms" json:"ttft_ms"`
	Durationms         int       `gorm:"column:duration_ms" json:"duration_ms"`
	ErrorType          string    `gorm:"column:error_type" json:"error_type"`
	BlockedBy          string    `gorm:"column:blocked_by" json:"blocked_by"`
	Fallback           bool      `gorm:"column:fallback" json:"fallback"`
	RequestID          string    `gorm:"column:request_id" json:"request_id"`
	SessionID          string    `gorm:"column:session_id" json:"session_id"`
	TraceID            string    `gorm:"column:trace_id" json:"trace_id"`
	SessionSource      string    `gorm:"column:session_source" json:"session_source"`
	AgentType          string    `gorm:"column:agent_type" json:"agent_type"`
	CacheHit           bool      `gorm:"column:cache_hit" json:"cache_hit"`
	CacheTier          string    `gorm:"column:cache_tier" json:"cache_tier"`
	CacheSource        string    `gorm:"column:cache_source" json:"cache_source"`
	CachedPromptTokens int       `gorm:"column:cached_prompt_tokens" json:"cached_prompt_tokens"`
	UpstreamRequestID  string    `gorm:"column:upstream_request_id" json:"upstream_request_id"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
}

const requestLogCols = `id, tenant, group_name, api_key_id, provider,
       model_requested, model_resolved, stream,
       prompt_tokens, completion_tokens, total_tokens,
       ttft_ms, duration_ms, error_type, blocked_by, fallback,
       request_id, session_id, trace_id, session_source, agent_type,
       cache_hit, cache_tier, cache_source, cached_prompt_tokens,
       upstream_request_id, created_at`

// buildRequestLogWhere assembles request-log filter predicates.
func buildRequestLogWhere(f RequestLogFilter) (where []string, args []any) {
	where = []string{"1=1"}
	if f.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.ModelRequested != "" {
		where = append(where, "model_requested = ?")
		args = append(args, f.ModelRequested)
	}
	if f.ErrorType != "" {
		where = append(where, "error_type = ?")
		args = append(args, f.ErrorType)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if f.AgentType != "" {
		where = append(where, "agent_type = ?")
		args = append(args, f.AgentType)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To)
	}
	return where, args
}

// ListRequestLogs returns an offset page of request-log rows in
// (created_at, id) DESC order. page is 1-based; pageSize <= 0 defaults to 50.
func (r *QueryRepo) ListRequestLogs(ctx context.Context, f RequestLogFilter, page, pageSize int) ([]RequestLogView, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}

	where, args := buildRequestLogWhere(f)
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM request_logs WHERE `+whereSQL, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	pageArgs := append(args, pageSize, (page-1)*pageSize)
	q := `SELECT ` + requestLogCols + `
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ? OFFSET ?`

	var rows []RequestLogView
	if err := r.db.WithContext(ctx).Raw(q, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []RequestLogView{}
	}
	return rows, total, nil
}

// ---------------------------------------------------------------------------
// sessions (aggregation over request_logs)
// ---------------------------------------------------------------------------

// SessionListFilter narrows a session-list aggregation. Empty fields ignored.
// A time window is strongly advised to bound the GROUP BY scan.
type SessionListFilter struct {
	AgentType string
	From, To  time.Time
}

// SessionSummary is one row of the session-list aggregation: per-session totals
// over request_logs (token sums, duration, request count, time bounds, latest
// non-empty agent type, has-errors flag). Drives the trace UI's session list.
type SessionSummary struct {
	SessionID        string    `json:"session_id"`
	AgentType        string    `json:"agent_type"`
	RequestCount     int       `json:"request_count"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	DurationMs       int       `json:"duration_ms"`
	StartedAt        time.Time `json:"started_at"`
	LastSeen         time.Time `json:"last_seen"`
	HasErrors        bool      `json:"has_errors"`
}

// sessionAggRow is the raw aggregation scan target: SQLite returns the
// has-errors flag as an integer (MAX(CASE...)), which we convert to bool when
// mapping to SessionSummary.
type sessionAggRow struct {
	SessionID        string    `gorm:"column:session_id"`
	RequestCount     int       `gorm:"column:request_count"`
	PromptTokens     int       `gorm:"column:prompt_tokens"`
	CompletionTokens int       `gorm:"column:completion_tokens"`
	TotalTokens      int       `gorm:"column:total_tokens"`
	DurationMs       int       `gorm:"column:duration_ms"`
	StartedAt        time.Time `gorm:"column:started_at"`
	LastSeen         time.Time `gorm:"column:last_seen"`
	AgentType        string    `gorm:"column:agent_type"`
	HasErrors        int       `gorm:"column:has_errors"`
}

// ListSessions aggregates request_logs by session_id, returning per-session
// totals ordered by most-recent-activity DESC. Only rows with a non-empty
// session_id are grouped.
func (r *QueryRepo) ListSessions(ctx context.Context, f SessionListFilter, page, pageSize int) ([]SessionSummary, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	where := []string{"session_id <> ''"}
	var args []any
	if f.AgentType != "" {
		where = append(where, "agent_type = ?")
		args = append(args, f.AgentType)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM (SELECT 1 FROM request_logs WHERE `+whereSQL+` GROUP BY session_id) s`, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	// agent_type: most-recent non-empty value per session (correlated
	// subquery — SQLite has no array_agg(...) FILTER like PostgreSQL).
	// has_errors: MAX(CASE ...) instead of PostgreSQL bool_or(...).
	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	q := `SELECT session_id,
	             COUNT(*)                                          AS request_count,
	             COALESCE(SUM(prompt_tokens), 0)                   AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0)               AS completion_tokens,
	             COALESCE(SUM(total_tokens), 0)                     AS total_tokens,
	             COALESCE(SUM(duration_ms), 0)                      AS duration_ms,
	             (SELECT created_at FROM request_logs r3
	                       WHERE r3.session_id = request_logs.session_id
	                       ORDER BY created_at ASC, id ASC LIMIT 1)  AS started_at,
	             (SELECT created_at FROM request_logs r4
	                       WHERE r4.session_id = request_logs.session_id
	                       ORDER BY created_at DESC, id DESC LIMIT 1) AS last_seen,
	             COALESCE((SELECT agent_type FROM request_logs r2
	                       WHERE r2.session_id = request_logs.session_id
	                         AND agent_type <> ''
	                       ORDER BY created_at DESC, id DESC LIMIT 1), '') AS agent_type,
	             MAX(CASE WHEN error_type <> '' THEN 1 ELSE 0 END)  AS has_errors
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      GROUP BY session_id
	      ORDER BY last_seen DESC
	      LIMIT ? OFFSET ?`

	var agg []sessionAggRow
	if err := r.db.WithContext(ctx).Raw(q, listArgs...).Scan(&agg).Error; err != nil {
		return nil, 0, err
	}
	out := make([]SessionSummary, 0, len(agg))
	for _, a := range agg {
		out = append(out, SessionSummary{
			SessionID:        a.SessionID,
			AgentType:        a.AgentType,
			RequestCount:     a.RequestCount,
			PromptTokens:     a.PromptTokens,
			CompletionTokens: a.CompletionTokens,
			TotalTokens:      a.TotalTokens,
			DurationMs:       a.DurationMs,
			StartedAt:        a.StartedAt,
			LastSeen:         a.LastSeen,
			HasErrors:        a.HasErrors != 0,
		})
	}
	return out, total, nil
}

// ---------------------------------------------------------------------------
// trace_payloads
// ---------------------------------------------------------------------------

// TraceSummary is one trace event for a list/timeline view — summary dimensions
// only, WITHOUT the large message/raw bodies (fetched on demand by
// GetTraceByRowID / GetTraceByRequestID).
type TraceSummary struct {
	ID             int64     `gorm:"column:id" json:"id"`
	RequestID      string    `gorm:"column:request_id" json:"request_id"`
	SessionID      string    `gorm:"column:session_id" json:"session_id"`
	TraceID        string    `gorm:"column:trace_id" json:"trace_id"`
	Tenant         string    `gorm:"column:tenant" json:"tenant"`
	Provider       string    `gorm:"column:provider" json:"provider"`
	ModelRequested string    `gorm:"column:model_requested" json:"model_requested"`
	Stream         bool      `gorm:"column:stream" json:"stream"`
	AgentType      string    `gorm:"column:agent_type" json:"agent_type"`
	StatusCode     int       `gorm:"column:status_code" json:"status_code"`
	StopReason     string    `gorm:"column:stop_reason" json:"stop_reason"`
	NMessages      int       `gorm:"column:n_messages" json:"n_messages"`
	NToolUse       int       `gorm:"column:n_tool_use" json:"n_tool_use"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"created_at"`
}

// TraceDetail is the full payload for a single request: the message + raw
// layers. messages/request_raw are JSON text emitted verbatim as json.RawMessage;
// response_raw/error_raw are TEXT (a verbatim SSE transcript) returned as string.
type TraceDetail struct {
	TraceSummary
	Messages    json.RawMessage `gorm:"column:messages" json:"messages"`
	RequestRaw  json.RawMessage `gorm:"column:request_raw" json:"request_raw"`
	ResponseRaw string          `gorm:"column:response_raw" json:"response_raw"`
	ErrorRaw    string          `gorm:"column:error_raw" json:"error_raw"`
}

// traceDetailScan is the raw scan target. The glebarez/sqlite driver cannot scan
// a TEXT column into json.RawMessage directly, so the message/raw bodies are
// read as strings and then wrapped in json.RawMessage for verbatim JSON output.
type traceDetailScan struct {
	TraceSummary
	Messages    string `gorm:"column:messages"`
	RequestRaw  string `gorm:"column:request_raw"`
	ResponseRaw string `gorm:"column:response_raw"`
	ErrorRaw    string `gorm:"column:error_raw"`
}

func toTraceDetail(s traceDetailScan) *TraceDetail {
	return &TraceDetail{
		TraceSummary: s.TraceSummary,
		Messages:     rawMessageOrNull(s.Messages),
		RequestRaw:   rawMessageOrNull(s.RequestRaw),
		ResponseRaw:  s.ResponseRaw,
		ErrorRaw:     s.ErrorRaw,
	}
}

// rawMessageOrNull normalizes a TEXT column rendered as json.RawMessage. An
// empty string (capture missed / marshal failed / upstream 4xx before parse)
// would otherwise produce json.RawMessage(""), which is invalid JSON and makes
// encoding/json fail the entire TraceDetail marshal — surfacing in the desktop
// trace viewer as "Unexpected end of JSON input". Map empty → "null" so the
// API always emits a valid JSON document.
//
// A non-empty but invalid JSON value (truncated capture, older binary writing
// raw bytes, encoding glitch) triggers the same failure path. Rather than
// silently degrading to null and losing the evidence, wrap the raw text in a
// sentinel object {"_invalid": <original>} so the desktop trace viewer can
// surface the original payload for debugging while still emitting valid JSON.
//
// Only Messages / RequestRaw go through this helper. ResponseRaw / ErrorRaw
// are declared as plain `string` on TraceDetail, so encoding/json emits them
// as JSON string literals — arbitrary bytes (including invalid JSON text)
// marshal cleanly without any normalization. The asymmetry is intentional:
// it preserves the original payload for debugging in the trace viewer,
// whereas Messages / RequestRaw must be parseable JSON for the structured
// view to render, so unparseable content degrades to the sentinel there.
func rawMessageOrNull(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("null")
	}
	if !json.Valid([]byte(s)) {
		wrapped, err := json.Marshal(map[string]string{"_invalid": s})
		if err != nil {
			// json.Marshal on map[string]string cannot fail; defensive.
			return json.RawMessage("null")
		}
		return json.RawMessage(wrapped)
	}
	return json.RawMessage(s)
}

const traceSummaryCols = `id, request_id, session_id, trace_id, tenant,
       provider, model_requested, stream, agent_type,
       status_code, stop_reason, n_messages, n_tool_use, created_at`

// ListTraceBySession returns the trace events for a session_id in chronological
// ASC order (timeline order). Summary only — no bodies. Capped at limit.
func (r *QueryRepo) ListTraceBySession(ctx context.Context, sessionID string, limit int) ([]TraceSummary, error) {
	if sessionID == "" {
		return []TraceSummary{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + traceSummaryCols + `
	      FROM trace_payloads
	      WHERE session_id = ?
	      ORDER BY created_at ASC, id ASC
	      LIMIT ?`
	var rows []TraceSummary
	if err := r.db.WithContext(ctx).Raw(q, sessionID, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []TraceSummary{}
	}
	return rows, nil
}

// GetTraceByRowID returns the full payload for a single row by primary key id
// (ADR-0040 — preferred over request_id when a session may carry duplicate
// request-ids). ok=false when no row matches.
func (r *QueryRepo) GetTraceByRowID(ctx context.Context, rowID int64) (*TraceDetail, bool, error) {
	if rowID <= 0 {
		return nil, false, nil
	}
	q := `SELECT ` + traceSummaryCols + `, messages, request_raw, response_raw, error_raw
	      FROM trace_payloads
	      WHERE id = ?
	      LIMIT 1`
	var s traceDetailScan
	if err := r.db.WithContext(ctx).Raw(q, rowID).Scan(&s).Error; err != nil {
		return nil, false, err
	}
	return toTraceDetail(s), s.ID != 0, nil
}

// GetTraceByRequestID returns the full payload for a single request_id (LIMIT 1
// — returns one row even if request_id is duplicated across sessions).
func (r *QueryRepo) GetTraceByRequestID(ctx context.Context, requestID string) (*TraceDetail, bool, error) {
	if requestID == "" {
		return nil, false, nil
	}
	q := `SELECT ` + traceSummaryCols + `, messages, request_raw, response_raw, error_raw
	      FROM trace_payloads
	      WHERE request_id = ?
	      ORDER BY created_at DESC, id DESC
	      LIMIT 1`
	var s traceDetailScan
	if err := r.db.WithContext(ctx).Raw(q, requestID).Scan(&s).Error; err != nil {
		return nil, false, err
	}
	return toTraceDetail(s), s.ID != 0, nil
}

// ---------------------------------------------------------------------------
// overview (per-agent rollup)
// ---------------------------------------------------------------------------

// AgentUsage is the per-agent rollup shown on the overview page: call volume,
// token sums, latency sums, and error count. Averages are derived by the UI.
type AgentUsage struct {
	AgentType        string `json:"agent_type"`
	RequestCount     int    `json:"request_count"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	DurationMs       int    `json:"duration_ms"`
	TTFTms           int    `json:"ttft_ms"`
	ErrorCount       int    `json:"error_count"`
}

// agentUsageAgg is the raw scan target (error_count is an integer aggregate).
type agentUsageAgg struct {
	AgentType        string `gorm:"column:agent_type"`
	RequestCount     int    `gorm:"column:request_count"`
	PromptTokens     int    `gorm:"column:prompt_tokens"`
	CompletionTokens int    `gorm:"column:completion_tokens"`
	TotalTokens      int    `gorm:"column:total_tokens"`
	DurationMs       int    `gorm:"column:duration_ms"`
	TTFTms           int    `gorm:"column:ttft_ms"`
	ErrorCount       int    `gorm:"column:error_count"`
}

// Overview returns a per-agent rollup (ordered by request count DESC) plus a
// grand-total row (AgentType == "ALL") over the optional time window. The
// desktop gateway does not run billing, so currency cost is intentionally
// absent (design/desktop.md §10.3: cost is "顺手记", tracked as token volume).
func (r *QueryRepo) Overview(ctx context.Context, from, to time.Time) ([]AgentUsage, AgentUsage, error) {
	where := []string{"1=1"}
	var args []any
	if !from.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, from)
	}
	if !to.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, to)
	}
	whereSQL := strings.Join(where, " AND ")

	q := `SELECT agent_type,
	             COUNT(*)                                          AS request_count,
	             COALESCE(SUM(prompt_tokens), 0)                   AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0)               AS completion_tokens,
	             COALESCE(SUM(total_tokens), 0)                     AS total_tokens,
	             COALESCE(SUM(duration_ms), 0)                      AS duration_ms,
	             COALESCE(SUM(ttft_ms), 0)                          AS ttft_ms,
	             COALESCE(SUM(CASE WHEN error_type <> '' THEN 1 ELSE 0 END), 0) AS error_count
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      GROUP BY agent_type
	      ORDER BY request_count DESC`

	var agg []agentUsageAgg
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&agg).Error; err != nil {
		return nil, AgentUsage{}, err
	}

	agents := make([]AgentUsage, 0, len(agg))
	var tot AgentUsage
	tot.AgentType = "ALL"
	for _, a := range agg {
		agents = append(agents, AgentUsage{
			AgentType:        a.AgentType,
			RequestCount:     a.RequestCount,
			PromptTokens:     a.PromptTokens,
			CompletionTokens: a.CompletionTokens,
			TotalTokens:      a.TotalTokens,
			DurationMs:       a.DurationMs,
			TTFTms:           a.TTFTms,
			ErrorCount:       a.ErrorCount,
		})
		tot.RequestCount += a.RequestCount
		tot.PromptTokens += a.PromptTokens
		tot.CompletionTokens += a.CompletionTokens
		tot.TotalTokens += a.TotalTokens
		tot.DurationMs += a.DurationMs
		tot.TTFTms += a.TTFTms
		tot.ErrorCount += a.ErrorCount
	}
	if agents == nil {
		agents = []AgentUsage{}
	}
	return agents, tot, nil
}
