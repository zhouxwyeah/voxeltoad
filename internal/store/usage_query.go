package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// UsageQueryRepo is the read side of usage_records for the management plane
// (ADR-0019). It is READ-ONLY (writes go through UsageRepo on the data plane).
// The tenant is bound at construction: a non-empty tenant scopes every query to
// that tenant's rows (structural isolation for tenant-admin reads); an empty
// tenant is the global view (super-admin). There is no method to widen the
// scope, mirroring TenantRepo (ADR-0017 §3).
type UsageQueryRepo struct {
	db     *DB
	tenant string // "" = global (super-admin)
}

// NewUsageQueryRepo builds a read repo. tenant=="" means global.
func NewUsageQueryRepo(db *DB, tenant string) *UsageQueryRepo {
	return &UsageQueryRepo{db: db, tenant: tenant}
}

// UsageRow is one usage record as returned to the management plane.
type UsageRow struct {
	ID                  int64     `json:"id"`
	Tenant              string    `json:"tenant"`
	GroupName           string    `json:"group_name"`
	APIKeyID            string    `json:"api_key_id"`
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	Cost                int64     `json:"cost"`
	CachedPromptTokens  int       `json:"cached_prompt_tokens"`
	CacheDiscountMicros int64     `json:"cache_discount_micros"`
	CreatedAt           time.Time `json:"created_at"`
}

// UsageSummaryRow is one aggregate bucket for the requested group_by dimension.
type UsageSummaryRow struct {
	GroupKey         string `json:"group_key"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Cost             int64  `json:"cost"`
	RequestCount     int64  `json:"request_count"`
}

// summaryDimensions whitelists the columns a caller may GROUP BY. This is a
// closed set (never interpolated from raw input) so the query is injection-safe.
var summaryDimensions = map[string]string{
	"tenant":     "tenant",
	"group_name": "group_name",
	"api_key_id": "api_key_id",
	"provider":   "provider",
	"model":      "model",
}

// UsageFilter narrows a usage query. Empty fields are ignored.
type UsageFilter struct {
	Provider string
	Model    string
	From, To time.Time
}

// List returns a page of usage rows in (created_at, id) DESC order, bounded by
// an optional [from, to) time range, provider/model filter, and the bound
// tenant. cursor is an opaque keyset cursor from a prior call (empty for the
// first page); the returned nextCursor is "" when there are no further pages.
func (r *UsageQueryRepo) List(ctx context.Context, f UsageFilter, cursor string, limit int) ([]UsageRow, string, error) {
	if limit <= 0 {
		limit = 50
	}

	where := []string{"1=1"}
	args := []any{}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	if f.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		where = append(where, "model = ?")
		args = append(args, f.Model)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To)
	}
	// Keyset: rows strictly "after" (older than) the cursor in DESC order.
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
	q := `SELECT id, tenant, group_name, api_key_id, provider, model,
	             prompt_tokens, completion_tokens, cost,
	             cached_prompt_tokens, cache_discount_micros, created_at
	      FROM usage_records
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ?`

	var rows []UsageRow
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
		rows = []UsageRow{} // marshal as [] not null (list envelope contract)
	}
	return rows, next, nil
}

// Summary groups usage by the given dimension over an optional [from, to) range
// and the bound tenant, summing cost/tokens and counting requests. groupBy must
// be one of the whitelisted dimensions.
func (r *UsageQueryRepo) Summary(ctx context.Context, from, to time.Time, groupBy string) ([]UsageSummaryRow, error) {
	col, ok := summaryDimensions[groupBy]
	if !ok {
		return nil, fmt.Errorf("usage summary: unsupported group_by %q", groupBy)
	}

	where := []string{"1=1"}
	args := []any{}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	if !from.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, from)
	}
	if !to.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, to)
	}

	// col is from the whitelist, safe to interpolate.
	q := `SELECT ` + col + ` AS group_key,
	             COALESCE(SUM(prompt_tokens), 0)     AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
	             COALESCE(SUM(cost), 0)              AS cost,
	             COUNT(*)                            AS request_count
	      FROM usage_records
	      WHERE ` + strings.Join(where, " AND ") + `
	      GROUP BY ` + col + `
	      ORDER BY cost DESC`

	var rows []UsageSummaryRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []UsageSummaryRow{} // marshal as [] not null (list envelope contract)
	}
	return rows, nil
}

// timeBuckets whitelists the date_trunc precisions a caller may request. Closed
// set so the bucket argument is never interpolated from raw input (injection-safe).
var timeBuckets = map[string]string{
	"hour": "hour",
	"day":  "day",
	"week": "week",
}

// UsageBucket is one time-bucketed aggregate for the timeseries endpoint.
type UsageBucket struct {
	BucketStart      time.Time `json:"bucket_start"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	Cost             int64     `json:"cost"`
	RequestCount     int64     `json:"request_count"`
}

// Timeseries returns cost/tokens/request-count bucketed by the given time
// precision (hour/day/week) over an optional [from, to) range. The bound tenant
// scope applies. Buckets with zero activity are NOT included (sparse output) —
// the frontend fills gaps as needed. Uses date_trunc on created_at.
func (r *UsageQueryRepo) Timeseries(ctx context.Context, f UsageFilter, bucket string) ([]UsageBucket, error) {
	trunc, ok := timeBuckets[bucket]
	if !ok {
		return nil, fmt.Errorf("usage timeseries: unsupported bucket %q (want hour/day/week)", bucket)
	}

	where := []string{"1=1"}
	args := []any{}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	if f.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		where = append(where, "model = ?")
		args = append(args, f.Model)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To)
	}

	q := `SELECT date_trunc(?, created_at)                AS bucket_start,
	             COALESCE(SUM(prompt_tokens), 0)         AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0)     AS completion_tokens,
	             COALESCE(SUM(cost), 0)                  AS cost,
	             COUNT(*)                                AS request_count
	      FROM usage_records
	      WHERE ` + strings.Join(where, " AND ") + `
	      GROUP BY bucket_start
	      ORDER BY bucket_start ASC`
	// Prepend the trunc literal as the first ?.
	args = append([]any{trunc}, args...)

	var rows []UsageBucket
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []UsageBucket{}
	}
	return rows, nil
}

// SessionCostSummary aggregates the cost/tokens of all usage_records rows for a
// single session_id. Used by the session-trace view to show "what did this
// session cost?" alongside the request timeline.
type SessionCostSummary struct {
	SessionID        string `json:"session_id"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Cost             int64  `json:"cost"`
	RequestCount     int64  `json:"request_count"`
}

// SummaryBySession aggregates cost/tokens for all usage_records rows matching
// sessionID. The bound tenant scope still applies. session_id must be non-empty.
// Hits idx_usage_records_session_created (migration 00014).
func (r *UsageQueryRepo) SummaryBySession(ctx context.Context, sessionID string) (SessionCostSummary, error) {
	var s SessionCostSummary
	if sessionID == "" {
		return s, nil
	}

	where := []string{"session_id = ?"}
	args := []any{sessionID}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}

	q := `SELECT ? AS session_id,
	             COALESCE(SUM(prompt_tokens), 0)     AS prompt_tokens,
	             COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
	             COALESCE(SUM(cost), 0)              AS cost,
	             COUNT(*)                            AS request_count
	      FROM usage_records
	      WHERE ` + strings.Join(where, " AND ")

	// Prepend sessionID as the first ? (the literal SELECT col).
	args = append([]any{sessionID}, args...)
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&s).Error; err != nil {
		return s, err
	}
	return s, nil
}

// SessionCost is one session's cost total from usage_records, used by the
// session-list view to merge cost onto the request_logs aggregation.
type SessionCost struct {
	SessionID string `json:"session_id"`
	Cost      int64  `json:"cost"`
}

// CostBySessions returns a map of session_id → total cost (micro-units) for the
// given session_ids. Used to batch-merge cost onto a session-list page (avoids
// an N+1 of SummaryBySession). The bound tenant scope applies. Sessions with no
// usage rows are absent from the map (treated as 0 cost by the caller).
func (r *UsageQueryRepo) CostBySessions(ctx context.Context, sessionIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return out, nil
	}

	where := []string{"session_id IN ?"}
	args := []any{sessionIDs}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}

	q := `SELECT session_id, COALESCE(SUM(cost), 0) AS cost
	      FROM usage_records
	      WHERE ` + strings.Join(where, " AND ") + `
	      GROUP BY session_id`

	var rows []SessionCost
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.SessionID] = row.Cost
	}
	return out, nil
}

// encodeUsageCursor packs (created_at, id) into an opaque base64 token.
func encodeUsageCursor(t time.Time, id int64) string {
	raw := fmt.Sprintf("%d:%d", t.UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeUsageCursor unpacks a token produced by encodeUsageCursor.
func decodeUsageCursor(s string) (time.Time, int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("usage cursor: %w", err)
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("usage cursor: malformed")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("usage cursor ts: %w", err)
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("usage cursor id: %w", err)
	}
	return time.Unix(0, ns).UTC(), id, nil
}
