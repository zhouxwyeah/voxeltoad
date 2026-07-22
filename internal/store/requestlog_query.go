package store

import (
	"context"
	"strings"
	"time"
)

// RequestLogQueryRepo is the read side of request_logs for the management
// plane (ADR-0021 §7, following the ADR-0019 read-API pattern established by
// UsageQueryRepo/AuditQueryRepo). READ-ONLY (writes go through RequestLogRepo
// on the data plane). Tenant is bound at construction: a non-empty tenant
// scopes every query to that tenant's rows (structural isolation for
// tenant-admin reads); an empty tenant is the global view (super-admin).
// There is no method to widen the scope.
type RequestLogQueryRepo struct {
	db     *DB
	tenant string // "" = global (super-admin)
}

// NewRequestLogQueryRepo builds a read repo. tenant=="" means global.
func NewRequestLogQueryRepo(db *DB, tenant string) *RequestLogQueryRepo {
	return &RequestLogQueryRepo{db: db, tenant: tenant}
}

// RequestLogFilter narrows a request-log query. Empty fields are ignored.
// Tenant/GroupName/APIKeyID are additional drill-down filters for the advanced
// search UI (P1); the bound tenant scope (NewRequestLogQueryRepo) still applies
// structurally — a tenant-admin cannot widen its view via these fields.
type RequestLogFilter struct {
	Provider          string
	ModelRequested    string
	ErrorType         string
	BlockedBy         string
	Tenant            string // super-admin only; ignored for tenant-admin (scoped)
	GroupName         string
	APIKeyID          string
	Stream            *bool // nil = no filter; true/false = exact match
	Fallback          *bool
	AgentType         string
	IngressProtocol   string // "openai" / "anthropic"; "" = no filter
	SessionID         string // session-scoped queries (debug/cost-attribution UIs)
	RequestID         string // exact per-request lookup (debug/trace drill-down)
	ClientRequestID   string // reverse lookup by client-supplied id (support: trace a client-side id to a gateway request)
	UpstreamRequestID string // reverse lookup by provider-assigned id (support/reconciliation)
	From, To          time.Time
}

// RequestLogRow is one request-audit entry as returned to the management
// plane, mirroring the request_logs columns (internal/store/migrations/
// 00004_request_logs.sql).
type RequestLogRow struct {
	ID                 int64     `json:"id"`
	Tenant             string    `json:"tenant"`
	GroupName          string    `json:"group_name"`
	APIKeyID           string    `json:"api_key_id"`
	Provider           string    `json:"provider"`
	ModelRequested     string    `json:"model_requested"`
	ModelResolved      string    `json:"model_resolved"`
	Stream             bool      `json:"stream"`
	PromptTokens       int       `json:"prompt_tokens"`
	CompletionTokens   int       `json:"completion_tokens"`
	TotalTokens        int       `json:"total_tokens"`
	TTFTms             int       `gorm:"column:ttft_ms" json:"ttft_ms"`
	Durationms         int       `gorm:"column:duration_ms" json:"duration_ms"`
	ErrorType          string    `json:"error_type"`
	BlockedBy          string    `json:"blocked_by"`
	Fallback           bool      `json:"fallback"`
	RequestID          string    `json:"request_id"`
	ClientRequestID    string    `json:"client_request_id"`
	SessionID          string    `json:"session_id"`
	TraceID            string    `json:"trace_id"`
	SessionSource      string    `json:"session_source"`
	AgentType          string    `json:"agent_type"`
	IngressProtocol    string    `json:"ingress_protocol"`
	CacheHit           bool      `json:"cache_hit"`
	CacheTier          string    `json:"cache_tier"`
	CacheSource        string    `json:"cache_source"`
	CachedPromptTokens int       `json:"cached_prompt_tokens"`
	UpstreamRequestID  string    `json:"upstream_request_id"`
	CreatedAt          time.Time `json:"created_at"`
}

// buildWhere assembles the filter WHERE clauses (without the keyset cursor
// predicate) so List and ListPage/Count share the same scoping logic. The bound
// tenant is always applied first; for a global repo (super-admin) f.Tenant is an
// additional drill-down. A tenant-admin cannot widen its scope via f.Tenant.
func (r *RequestLogQueryRepo) buildWhere(f RequestLogFilter) (where []string, args []any) {
	where = []string{"1=1"}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
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
	if f.BlockedBy != "" {
		where = append(where, "blocked_by = ?")
		args = append(args, f.BlockedBy)
	}
	// Advanced drill-down filters (P1). Tenant is only applied when the repo
	// is global (super-admin); for a tenant-admin the structural scope already
	// applies and f.Tenant is redundant/ignored.
	if r.tenant == "" && f.Tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, f.Tenant)
	}
	if f.GroupName != "" {
		where = append(where, "group_name = ?")
		args = append(args, f.GroupName)
	}
	if f.APIKeyID != "" {
		where = append(where, "api_key_id = ?")
		args = append(args, f.APIKeyID)
	}
	if f.Stream != nil {
		where = append(where, "stream = ?")
		args = append(args, *f.Stream)
	}
	if f.Fallback != nil {
		where = append(where, "fallback = ?")
		args = append(args, *f.Fallback)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, f.RequestID)
	}
	if f.ClientRequestID != "" {
		where = append(where, "client_request_id = ?")
		args = append(args, f.ClientRequestID)
	}
	if f.UpstreamRequestID != "" {
		where = append(where, "upstream_request_id = ?")
		args = append(args, f.UpstreamRequestID)
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

// List returns a page of request-log rows in (created_at, id) DESC order,
// bounded by an optional [from, to) time range, the bound tenant, and the
// given filter. cursor is an opaque keyset cursor from a prior call (empty for
// the first page); the returned nextCursor is "" when there are no further
// pages. limit caps the page size. Hits idx_request_logs_tenant_created /
// idx_request_logs_created_at (no new migration needed).
func (r *RequestLogQueryRepo) List(ctx context.Context, f RequestLogFilter, cursor string, limit int) ([]RequestLogRow, string, error) {
	if limit <= 0 {
		limit = 50
	}

	where, args := r.buildWhere(f)
	// Keyset: rows strictly "after" (older than) the cursor in DESC order.
	// Reuses the same (created_at,id) cursor encoding as UsageQueryRepo/
	// AuditQueryRepo — the encoding is generic, not usage-specific.
	if cursor != "" {
		ct, cid, err := decodeUsageCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(created_at, id) < (?, ?)")
		args = append(args, ct, cid)
	}

	// Fetch limit+1 to detect whether another page exists.
	args = append(args, limit+1)
	q := `SELECT id, tenant, group_name, api_key_id, provider,
	             model_requested, model_resolved, stream,
	             prompt_tokens, completion_tokens, total_tokens,
	             ttft_ms, duration_ms, error_type, blocked_by, fallback,
	             request_id, client_request_id, session_id, trace_id, session_source, agent_type, ingress_protocol,
	             cache_hit, cache_tier, cache_source, cached_prompt_tokens,
	             upstream_request_id, created_at
	      FROM request_logs
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ?`

	var rows []RequestLogRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	next := ""
	if len(rows) > limit {
		last := rows[limit-1]
		next = encodeUsageCursor(last.CreatedAt, last.ID)
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []RequestLogRow{} // marshal as [] not null (list envelope contract)
	}
	return rows, next, nil
}

// ListPage returns a single offset-paginated page of request-log rows plus the
// total matching row count (for page-jump UIs). page is 1-based; pageSize must
// be > 0. COUNT(*) reuses buildWhere so the bound-tenant scope applies
// identically; the default [from,to) window on the UI keeps the cost bounded.
func (r *RequestLogQueryRepo) ListPage(ctx context.Context, f RequestLogFilter, page, pageSize int) ([]RequestLogRow, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}

	where, args := r.buildWhere(f)
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM request_logs WHERE `+whereSQL, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	pageArgs := append(args, pageSize, offset)
	q := `SELECT id, tenant, group_name, api_key_id, provider,
	             model_requested, model_resolved, stream,
	             prompt_tokens, completion_tokens, total_tokens,
	             ttft_ms, duration_ms, error_type, blocked_by, fallback,
	             request_id, client_request_id, session_id, trace_id, session_source, agent_type, ingress_protocol,
	             cache_hit, cache_tier, cache_source, cached_prompt_tokens,
	             upstream_request_id, created_at
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ? OFFSET ?`

	var rows []RequestLogRow
	if err := r.db.WithContext(ctx).Raw(q, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []RequestLogRow{} // marshal as [] not null
	}
	return rows, total, nil
}

// ListBySession returns all request-log rows for a given session_id in
// chronological ASC order (oldest first) — the timeline order a session-trace
// view needs. The bound tenant scope still applies (a tenant-admin sees only its
// own rows). session_id must be non-empty; empty session rows are never matched.
// Hits idx_request_logs_session_created. The result is capped at limit to bound
// cost on long-lived sessions.
func (r *RequestLogQueryRepo) ListBySession(ctx context.Context, sessionID string, limit int) ([]RequestLogRow, error) {
	if sessionID == "" {
		return []RequestLogRow{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	where, args := r.buildWhere(RequestLogFilter{SessionID: sessionID})
	whereSQL := strings.Join(where, " AND ")
	args = append(args, limit)

	q := `SELECT id, tenant, group_name, api_key_id, provider,
	             model_requested, model_resolved, stream,
	             prompt_tokens, completion_tokens, total_tokens,
	             ttft_ms, duration_ms, error_type, blocked_by, fallback,
	             request_id, client_request_id, session_id, trace_id, session_source, agent_type, ingress_protocol,
	             cache_hit, cache_tier, cache_source, cached_prompt_tokens,
	             upstream_request_id, created_at
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      ORDER BY created_at ASC, id ASC
	      LIMIT ?`

	var rows []RequestLogRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []RequestLogRow{}
	}
	return rows, nil
}

// SessionSummary is one row of the session-list aggregation: per-session
// totals over request_logs (tokens, duration, request count) plus the latest
// detected agent type. The Cost field is filled separately from usage_records
// by the handler (it lives in a different table) and is 0 when no usage rows
// matched. Drives the trace UI's session-list view.
type SessionSummary struct {
	SessionID        string    `json:"session_id"`
	AgentType        string    `json:"agent_type"`
	RequestCount     int       `json:"request_count"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	DurationMs       int       `gorm:"column:duration_ms" json:"duration_ms"`
	Cost             int64     `json:"cost"` // micro-units, merged from usage_records
	StartedAt        time.Time `json:"started_at"`
	LastSeen         time.Time `json:"last_seen"`
	HasErrors        bool      `json:"has_errors"`
}

// SessionListFilter narrows a session-list aggregation. Empty fields are
// ignored except the bound tenant scope (always applied). The default time
// window is bounded by the caller to keep the GROUP BY scan cheap.
type SessionListFilter struct {
	AgentType string
	From, To  time.Time
}

// ListSessions aggregates request_logs by session_id, returning per-session
// totals (request count, token sums, duration sum, time bounds, latest agent
// type, has-errors flag) ordered by most-recent-activity DESC. The Cost column
// is NOT populated here (cost lives in usage_records); the handler merges it in
// a second query. The bound tenant scope applies.
//
// Only rows with a non-empty session_id are grouped (empty-session rows are
// excluded — they cannot form a session). A time window is strongly advised to
// bound the GROUP BY scan over the partitioned table.
func (r *RequestLogQueryRepo) ListSessions(ctx context.Context, f SessionListFilter, page, pageSize int) ([]SessionSummary, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultSessionPageSize
	}
	offset := (page - 1) * pageSize

	where := []string{"session_id <> ''"}
	var args []any
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
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
	whereSQL := strings.Join(where, " AND ")

	// agent_type per session: take the most-recent non-empty value. array_agg
	// with FILTER keeps only non-empty agent types, ordered so [1] is newest.
	countArgs := append([]any{}, args...)
	var total int64
	countQ := `SELECT COUNT(*) FROM (SELECT 1 FROM request_logs WHERE ` + whereSQL + ` GROUP BY session_id) s`
	if err := r.db.WithContext(ctx).Raw(countQ, countArgs...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, offset)
	q := `SELECT session_id,
	             COUNT(*)                                          AS request_count,
	             COALESCE(SUM(prompt_tokens), 0)                   AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0)               AS completion_tokens,
	             COALESCE(SUM(total_tokens), 0)                     AS total_tokens,
	             COALESCE(SUM(duration_ms), 0)                      AS duration_ms,
	             MIN(created_at)                                    AS started_at,
	             MAX(created_at)                                    AS last_seen,
	             COALESCE((array_agg(agent_type ORDER BY created_at DESC) FILTER (WHERE agent_type <> ''))[1], '') AS agent_type,
	             bool_or(error_type <> '')                          AS has_errors
	      FROM request_logs
	      WHERE ` + whereSQL + `
	      GROUP BY session_id
	      ORDER BY MAX(created_at) DESC
	      LIMIT ? OFFSET ?`

	var rows []SessionSummary
	if err := r.db.WithContext(ctx).Raw(q, listArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []SessionSummary{}
	}
	return rows, total, nil
}

const defaultSessionPageSize = 20
