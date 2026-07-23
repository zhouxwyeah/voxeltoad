//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

// seedRequestLogAt inserts a request_logs row with explicit created_at and
// tenant/provider/error_type so keyset/time-range/filter behavior is
// deterministic.
func seedRequestLogAt(t *testing.T, db *store.DB, tenant, provider, errorType string, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO request_logs
		   (tenant, group_name, api_key_id, provider,
		    model_requested, model_resolved, stream,
		    prompt_tokens, completion_tokens, total_tokens,
		    ttft_ms, duration_ms, error_type, blocked_by, fallback,
		    request_id, session_id, trace_id, session_source, agent_type,
		    cache_hit, cache_tier, cache_source, cached_prompt_tokens,
		    upstream_request_id, ingress_protocol, provider_endpoint, created_at)
		 VALUES (?, '', 'k', ?, 'chat', 'gpt-4o', false, 10, 20, 30, 50, 100, ?, '', false,
		         'req-1', 'sess-1', '', '', '',
		         false, '', '', 0,
		         '', '', '', ?)`,
		tenant, provider, errorType, at,
	).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}
}

// A tenant-scoped query returns only that tenant's rows; the global (empty
// tenant) query sees everything. Structural isolation for reads (ADR-0019
// pattern, mirrored from UsageQueryRepo/AuditQueryRepo).
func TestRequestLogQuery_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()
	seedRequestLogAt(t, db, "acme", "openai", "", now)
	seedRequestLogAt(t, db, "acme", "openai", "", now.Add(-time.Minute))
	seedRequestLogAt(t, db, "other", "openai", "", now)

	scoped := store.NewRequestLogQueryRepo(db, "acme")
	rows, _, err := scoped.List(ctx, store.RequestLogFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List scoped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("scoped rows = %d, want 2 (acme only)", len(rows))
	}
	for _, r := range rows {
		if r.Tenant != "acme" {
			t.Errorf("scoped query leaked tenant %q", r.Tenant)
		}
		// Regression guard: the ttft_ms / duration_ms columns must map onto the
		// TTFTms / Durationms fields. These field names don't snake-case to the
		// column names, so without explicit gorm column tags gorm's default
		// naming silently leaves them 0. seedRequestLogAt writes 50 / 100.
		if r.TTFTms != 50 {
			t.Errorf("TTFTms = %d, want 50 (column mapping regression)", r.TTFTms)
		}
		if r.Durationms != 100 {
			t.Errorf("Durationms = %d, want 100 (column mapping regression)", r.Durationms)
		}
	}

	global := store.NewRequestLogQueryRepo(db, "")
	all, _, err := global.List(ctx, store.RequestLogFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List global: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("global rows = %d, want 3", len(all))
	}
}

// Keyset pagination walks the full set in (created_at, id) DESC order with no
// gaps or dupes, and the final page returns an empty next_cursor.
func TestRequestLogQuery_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	base := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		seedRequestLogAt(t, db, "acme", "openai", "", base.Add(-time.Duration(i)*time.Second))
	}

	repo := store.NewRequestLogQueryRepo(db, "acme")
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		rows, next, err := repo.List(ctx, store.RequestLogFilter{}, cursor, 2)
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Errorf("row id %d returned twice across pages", r.ID)
			}
			seen[r.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > total+2 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Errorf("saw %d distinct rows across pages, want %d", len(seen), total)
	}
}

// A time range bounds results: [from, to). Rows outside are excluded.
func TestRequestLogQuery_TimeRange(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	seedRequestLogAt(t, db, "acme", "openai", "", t0.Add(-time.Hour))   // before window
	seedRequestLogAt(t, db, "acme", "openai", "", t0.Add(time.Hour))    // inside
	seedRequestLogAt(t, db, "acme", "openai", "", t0.Add(48*time.Hour)) // after window (to is exclusive)

	repo := store.NewRequestLogQueryRepo(db, "acme")
	from := t0
	to := t0.Add(24 * time.Hour)
	rows, _, err := repo.List(ctx, store.RequestLogFilter{From: from, To: to}, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("time-ranged rows = %+v, want exactly the one inside [from,to)", rows)
	}
}

// Provider/error_type filters narrow the result set.
func TestRequestLogQuery_Filters(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()
	seedRequestLogAt(t, db, "acme", "openai", "", now)
	seedRequestLogAt(t, db, "acme", "claude", "", now.Add(-time.Second))
	seedRequestLogAt(t, db, "acme", "openai", "upstream_error", now.Add(-2*time.Second))

	repo := store.NewRequestLogQueryRepo(db, "acme")

	rows, _, err := repo.List(ctx, store.RequestLogFilter{Provider: "openai"}, "", 100)
	if err != nil {
		t.Fatalf("List provider filter: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("provider-filtered rows = %d, want 2", len(rows))
	}

	rows, _, err = repo.List(ctx, store.RequestLogFilter{ErrorType: "upstream_error"}, "", 100)
	if err != nil {
		t.Fatalf("List error_type filter: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("error_type-filtered rows = %d, want 1", len(rows))
	}
}

// session_id filter narrows results to a single session's request trail.
func TestRequestLogQuery_SessionIDFilter(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()

	// Insert rows with different session IDs by using raw SQL (seedRequestLogAt
	// hard-codes 'sess-1').
	seedWith := func(tenant, provider, sessionID string, at time.Time) {
		t.Helper()
		if err := db.Exec(
			`INSERT INTO request_logs
			   (tenant, group_name, api_key_id, provider,
			    model_requested, model_resolved, stream,
			    prompt_tokens, completion_tokens, total_tokens,
			    ttft_ms, duration_ms, error_type, blocked_by, fallback,
			    request_id, session_id, created_at)
			 VALUES (?, '', 'k', ?, 'chat', 'gpt-4o', false, 10, 20, 30, 50, 100, '', '', false, 'req-1', ?, ?)`,
			tenant, provider, sessionID, at,
		).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
	}
	seedWith("acme", "openai", "session-abc", now)
	seedWith("acme", "openai", "session-abc", now.Add(-time.Minute))
	seedWith("acme", "openai", "session-xyz", now.Add(-2*time.Minute))

	repo := store.NewRequestLogQueryRepo(db, "acme")
	rows, _, err := repo.List(ctx, store.RequestLogFilter{SessionID: "session-abc"}, "", 100)
	if err != nil {
		t.Fatalf("List session_id filter: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("session_id-filtered rows = %d, want 2 (session-abc)", len(rows))
	}
	for _, r := range rows {
		if r.SessionID != "session-abc" {
			t.Errorf("session_id filter leaked row with session_id = %q", r.SessionID)
		}
	}
}

// request_id filter narrows results to a single request (exact lookup). Mirrors
// TestRequestLogQuery_SessionIDFilter but exercises the new RequestID filter.
func TestRequestLogQuery_RequestIDFilter(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()

	seedWith := func(tenant, provider, requestID string, at time.Time) {
		t.Helper()
		if err := db.Exec(
			`INSERT INTO request_logs
			   (tenant, group_name, api_key_id, provider,
			    model_requested, model_resolved, stream,
			    prompt_tokens, completion_tokens, total_tokens,
			    ttft_ms, duration_ms, error_type, blocked_by, fallback,
			    request_id, session_id, created_at)
			 VALUES (?, '', 'k', ?, 'chat', 'gpt-4o', false, 10, 20, 30, 50, 100, '', '', false, ?, 'sess-1', ?)`,
			tenant, provider, requestID, at,
		).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
	}
	seedWith("acme", "openai", "req-1", now)
	seedWith("acme", "openai", "req-2", now.Add(-time.Minute))
	seedWith("acme", "openai", "req-3", now.Add(-2*time.Minute))

	repo := store.NewRequestLogQueryRepo(db, "acme")
	rows, _, err := repo.List(ctx, store.RequestLogFilter{RequestID: "req-2"}, "", 100)
	if err != nil {
		t.Fatalf("List request_id filter: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("request_id-filtered rows = %d, want 1 (req-2)", len(rows))
	}
	for _, r := range rows {
		if r.RequestID != "req-2" {
			t.Errorf("request_id filter leaked row with request_id = %q", r.RequestID)
		}
	}
}

// ListPage returns the requested offset window plus the total count, in
// (created_at, id) DESC order. The total respects filters and tenant scoping;
// an out-of-range page yields an empty slice but the total is still correct.
func TestRequestLogQuery_ListPage(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	base := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		seedRequestLogAt(t, db, "acme", "openai", "", base.Add(-time.Duration(i)*time.Second))
	}

	repo := store.NewRequestLogQueryRepo(db, "acme")

	rows, n, err := repo.ListPage(ctx, store.RequestLogFilter{}, 1, 2)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if n != total {
		t.Errorf("total = %d, want %d", n, total)
	}
	if len(rows) != 2 {
		t.Errorf("page 1 rows = %d, want 2", len(rows))
	}
	if !rows[0].CreatedAt.After(rows[1].CreatedAt) {
		t.Errorf("page not in DESC order: %v then %v", rows[0].CreatedAt, rows[1].CreatedAt)
	}

	// A tenant-scoped repo never counts another tenant's rows.
	seedRequestLogAt(t, db, "other", "openai", "", base)
	_, n2, err := repo.ListPage(ctx, store.RequestLogFilter{}, 1, 100)
	if err != nil {
		t.Fatalf("ListPage scoped: %v", err)
	}
	if n2 != total {
		t.Errorf("scoped total = %d, want %d (must not leak 'other')", n2, total)
	}

	// Paging through covers the full set without gaps/dupes.
	seen := map[int64]bool{}
	for p := 1; ; p++ {
		page, _, err := repo.ListPage(ctx, store.RequestLogFilter{}, p, 2)
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		if len(page) == 0 {
			break
		}
		for _, r := range page {
			if seen[r.ID] {
				t.Errorf("row %d duplicated across pages", r.ID)
			}
			seen[r.ID] = true
		}
	}
	if len(seen) != total {
		t.Errorf("paged through %d distinct rows, want %d", len(seen), total)
	}
}

// seedSessionRequest inserts a request_logs row with explicit session_id,
// agent_type, tokens, duration, error_type, and created_at for the session
// aggregation tests.
func seedSessionRequest(t *testing.T, db *store.DB, tenant, sessionID, agentType, errorType string, prompt, completion, total, durationMs int, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO request_logs
		   (tenant, group_name, api_key_id, provider,
		    model_requested, model_resolved, stream,
		    prompt_tokens, completion_tokens, total_tokens,
		    ttft_ms, duration_ms, error_type, blocked_by, fallback,
		    request_id, session_id, trace_id, session_source, agent_type, created_at)
		 VALUES (?, '', 'k', 'openai', 'chat', 'gpt-4o', false, ?, ?, ?, 0, ?, ?, '', false, '', ?, '', '', ?, ?)`,
		tenant, prompt, completion, total, durationMs, errorType, sessionID, agentType, at,
	).Error; err != nil {
		t.Fatalf("seed session request_logs: %v", err)
	}
}

// ListSessions aggregates per-session totals (tokens, duration, count), picks
// the latest non-empty agent_type, flags errors, orders by last activity DESC,
// and respects the bound tenant scope + agent filter + pagination.
func TestRequestLogQuery_ListSessions(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Session A: 2 requests, claude-code, one error.
	seedSessionRequest(t, db, "acme", "sess-A", "claude-code", "", 100, 50, 150, 1000, now.Add(-2*time.Minute))
	seedSessionRequest(t, db, "acme", "sess-A", "claude-code", "upstream_error", 200, 80, 280, 2000, now)

	// Session B: 1 request, codex, no error, more recent than A's last.
	seedSessionRequest(t, db, "acme", "sess-B", "codex", "", 50, 30, 80, 500, now.Add(30*time.Second))

	// Session in another tenant — must be invisible to acme.
	seedSessionRequest(t, db, "other", "sess-C", "claude-code", "", 10, 5, 15, 100, now.Add(time.Hour))

	// Empty-session row — must be excluded from the grouping.
	seedSessionRequest(t, db, "acme", "", "", "", 999, 0, 999, 0, now)

	repo := store.NewRequestLogQueryRepo(db, "acme")
	sessions, total, err := repo.ListSessions(ctx, store.SessionListFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (acme has sess-A and sess-B)", total)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	// Most-recent-activity first: sess-B (now+30s) then sess-A (now).
	if sessions[0].SessionID != "sess-B" || sessions[1].SessionID != "sess-A" {
		t.Fatalf("order = [%s, %s], want [sess-B, sess-A]", sessions[0].SessionID, sessions[1].SessionID)
	}

	// sess-A aggregates: 2 requests, tokens summed (300/130/430), duration 3000,
	// agent claude-code, has_errors true.
	a := sessions[1]
	if a.RequestCount != 2 {
		t.Errorf("sess-A request_count = %d, want 2", a.RequestCount)
	}
	if a.PromptTokens != 300 || a.CompletionTokens != 130 || a.TotalTokens != 430 {
		t.Errorf("sess-A tokens = %d/%d/%d, want 300/130/430", a.PromptTokens, a.CompletionTokens, a.TotalTokens)
	}
	if a.DurationMs != 3000 {
		t.Errorf("sess-A duration_ms = %d, want 3000", a.DurationMs)
	}
	if a.AgentType != "claude-code" {
		t.Errorf("sess-A agent_type = %q, want claude-code", a.AgentType)
	}
	if !a.HasErrors {
		t.Errorf("sess-A has_errors = false, want true")
	}

	// sess-B: 1 request, codex, no error.
	b := sessions[0]
	if b.AgentType != "codex" {
		t.Errorf("sess-B agent_type = %q, want codex", b.AgentType)
	}
	if b.HasErrors {
		t.Errorf("sess-B has_errors = true, want false")
	}
}

// The agent_type filter narrows the session list to one agent.
func TestRequestLogQuery_ListSessions_AgentFilter(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedSessionRequest(t, db, "acme", "sess-A", "claude-code", "", 10, 5, 15, 100, now)
	seedSessionRequest(t, db, "acme", "sess-B", "codex", "", 10, 5, 15, 100, now)

	repo := store.NewRequestLogQueryRepo(db, "acme")
	sessions, total, err := repo.ListSessions(ctx, store.SessionListFilter{AgentType: "claude-code"}, 1, 50)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 1 || len(sessions) != 1 || sessions[0].SessionID != "sess-A" {
		t.Fatalf("agent filter = %d rows [%v], want 1 [sess-A]", len(sessions), sessions)
	}
}
