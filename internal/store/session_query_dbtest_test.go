//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/billing"
	"voxeltoad/internal/store"
)

// seedSessionLog inserts a request_logs row with an explicit session_id,
// created_at and provider — so session-scoped timeline order is deterministic.
func seedSessionLog(t *testing.T, db *store.DB, sessionID, tenant, provider string, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO request_logs
		   (tenant, group_name, api_key_id, provider,
		    model_requested, model_resolved, stream,
		    prompt_tokens, completion_tokens, total_tokens,
		    ttft_ms, duration_ms, error_type, blocked_by, fallback,
		    request_id, session_id, created_at)
		 VALUES (?, '', 'k', ?, 'chat', 'gpt-4o', false, 10, 20, 30, 50, 100, '', '', false, ?, ?, ?)`,
		tenant, provider, "req-"+at.Format("150405"), sessionID, at,
	).Error; err != nil {
		t.Fatalf("seed session log: %v", err)
	}
}

// ListBySession returns rows for the given session in chronological ASC order
// (timeline order), and excludes rows from other sessions + empty-session rows.
func TestRequestLogQuery_ListBySession(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	base := time.Now().UTC().Add(-1 * time.Hour)

	// Three requests in session "abc", seeded out of order ( DESC time would
	// reverse them; ASC must put the earliest first).
	seedSessionLog(t, db, "abc", "acme", "openai", base.Add(2*time.Minute)) // 3rd
	seedSessionLog(t, db, "abc", "acme", "claude", base.Add(0))             // 1st
	seedSessionLog(t, db, "abc", "acme", "openai", base.Add(1*time.Minute)) // 2nd
	// A different session and an empty-session row must NOT appear.
	seedSessionLog(t, db, "xyz", "acme", "openai", base.Add(30*time.Second))
	seedSessionLog(t, db, "", "acme", "openai", base.Add(45*time.Second))

	repo := store.NewRequestLogQueryRepo(db, "acme")
	rows, err := repo.ListBySession(ctx, "abc", 100)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (session abc only)", len(rows))
	}
	// ASC order: earliest first.
	if !rows[0].CreatedAt.Before(rows[1].CreatedAt) || !rows[1].CreatedAt.Before(rows[2].CreatedAt) {
		t.Errorf("rows not in ASC created_at order: %v %v %v",
			rows[0].CreatedAt, rows[1].CreatedAt, rows[2].CreatedAt)
	}
	// All rows belong to session abc.
	for _, r := range rows {
		if r.SessionID != "abc" {
			t.Errorf("leaked session %q into abc trace", r.SessionID)
		}
	}
}

// ListBySession respects the bound tenant scope: a tenant-admin sees only its
// own session rows.
func TestRequestLogQuery_ListBySession_TenantScoped(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()
	seedSessionLog(t, db, "shared", "acme", "openai", now)
	seedSessionLog(t, db, "shared", "other", "openai", now)

	scoped := store.NewRequestLogQueryRepo(db, "acme")
	rows, err := scoped.ListBySession(ctx, "shared", 100)
	if err != nil {
		t.Fatalf("ListBySession scoped: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("scoped rows = %d, want 1 (acme only)", len(rows))
	}
	if rows[0].Tenant != "acme" {
		t.Errorf("scoped query leaked tenant %q", rows[0].Tenant)
	}
}

// Empty session_id returns nothing (no rows matched, since session_id="" rows
// are never returned by the empty-string guard).
func TestRequestLogQuery_ListBySession_EmptySession(t *testing.T) {
	ctx := context.Background()
	_, db := freshRequestLogRepo(t)
	now := time.Now().UTC()
	seedSessionLog(t, db, "", "acme", "openai", now)

	repo := store.NewRequestLogQueryRepo(db, "acme")
	rows, err := repo.ListBySession(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListBySession empty: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("empty session_id should return no rows; got %d", len(rows))
	}
}

// SummaryBySession aggregates cost/tokens/count for all usage_records rows in a
// session, respecting tenant scope.
func TestUsageQuery_SummaryBySession(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE usage_records`).Error; err != nil {
		t.Fatalf("truncate usage_records: %v", err)
	}
	usageRepo := store.NewUsageRepo(db)

	// Three billable records in session "abc" for tenant acme.
	records := []billing.UsageRecord{
		{Tenant: "acme", Provider: "openai", Model: "chat", PromptTokens: 1000, CompletionTokens: 500, Cost: 10000, SessionID: "abc", RequestID: "r1"},
		{Tenant: "acme", Provider: "claude", Model: "chat", PromptTokens: 2000, CompletionTokens: 1000, Cost: 20000, SessionID: "abc", RequestID: "r2"},
		{Tenant: "acme", Provider: "openai", Model: "chat", PromptTokens: 500, CompletionTokens: 250, Cost: 5000, SessionID: "abc", RequestID: "r3"},
	}
	if err := usageRepo.RecordBatch(ctx, records); err != nil {
		t.Fatalf("RecordBatch: %v", err)
	}
	// A record in a different session — must not be counted.
	if err := usageRepo.Record(ctx, billing.UsageRecord{
		Tenant: "acme", Provider: "openai", Model: "chat", Cost: 99999, SessionID: "xyz",
	}); err != nil {
		t.Fatalf("Record xyz: %v", err)
	}
	// A record in the same session but a different tenant — must not be counted
	// when scoped.
	if err := usageRepo.Record(ctx, billing.UsageRecord{
		Tenant: "other", Provider: "openai", Model: "chat", Cost: 88888, SessionID: "abc",
	}); err != nil {
		t.Fatalf("Record other: %v", err)
	}
	// An empty-session record — must not be counted.
	if err := usageRepo.Record(ctx, billing.UsageRecord{
		Tenant: "acme", Provider: "openai", Model: "chat", Cost: 77777, SessionID: "",
	}); err != nil {
		t.Fatalf("Record empty: %v", err)
	}

	// Scoped to acme.
	repo := store.NewUsageQueryRepo(db, "acme")
	summary, err := repo.SummaryBySession(ctx, "abc")
	if err != nil {
		t.Fatalf("SummaryBySession: %v", err)
	}
	if summary.RequestCount != 3 {
		t.Errorf("request_count = %d, want 3", summary.RequestCount)
	}
	if summary.PromptTokens != 3500 || summary.CompletionTokens != 1750 {
		t.Errorf("tokens = %d/%d, want 3500/1750", summary.PromptTokens, summary.CompletionTokens)
	}
	if summary.Cost != 35000 {
		t.Errorf("cost = %d, want 35000 (10000+20000+5000)", summary.Cost)
	}
	if summary.SessionID != "abc" {
		t.Errorf("session_id = %q, want abc", summary.SessionID)
	}

	// Global repo would also see the "other" tenant's row.
	global := store.NewUsageQueryRepo(db, "")
	summaryGlobal, err := global.SummaryBySession(ctx, "abc")
	if err != nil {
		t.Fatalf("SummaryBySession global: %v", err)
	}
	if summaryGlobal.RequestCount != 4 {
		t.Errorf("global request_count = %d, want 4 (acme 3 + other 1)", summaryGlobal.RequestCount)
	}
}

// SummaryBySession with an empty session_id returns a zero summary (no error).
func TestUsageQuery_SummaryBySession_EmptySession(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE usage_records`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}

	repo := store.NewUsageQueryRepo(db, "acme")
	summary, err := repo.SummaryBySession(ctx, "")
	if err != nil {
		t.Fatalf("SummaryBySession empty: %v", err)
	}
	if summary.RequestCount != 0 || summary.Cost != 0 {
		t.Errorf("empty session should give zero summary; got %+v", summary)
	}
}
