package desktopstore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// seedTestData inserts deterministic request_logs + trace_payloads rows so the
// read queries can be asserted against known values. Two agents (claude-code,
// codebuddy), three sessions, one session with an error.
func seedTestData(t *testing.T, db *DB) {
	t.Helper()
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)

	logs := []RequestLogRow{
		{ID: 1, Tenant: "default", Group: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", Stream: false, PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30, TTFTms: 100, Durationms: 500, RequestID: "req-1", SessionID: "sess-claude", SessionSource: "header", AgentType: "claude-code", CreatedAt: base},
		{ID: 2, Tenant: "default", Group: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", Stream: false, PromptTokens: 12, CompletionTokens: 18, TotalTokens: 30, TTFTms: 120, Durationms: 600, RequestID: "req-2", SessionID: "sess-claude", SessionSource: "header", AgentType: "claude-code", CreatedAt: base.Add(time.Minute)},
		{ID: 3, Tenant: "default", Group: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", Stream: false, PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5, TTFTms: 0, Durationms: 50, ErrorType: "upstream_error", RequestID: "req-3", SessionID: "sess-cb-err", SessionSource: "header", AgentType: "codebuddy", CreatedAt: base.Add(2 * time.Minute)},
		{ID: 4, Tenant: "default", Group: "default", Provider: "openai", ModelRequested: "default", ModelResolved: "gpt-4o-mini", Stream: false, PromptTokens: 8, CompletionTokens: 22, TotalTokens: 30, TTFTms: 90, Durationms: 400, RequestID: "req-4", SessionID: "sess-cb-ok", SessionSource: "header", AgentType: "codebuddy", CreatedAt: base.Add(3 * time.Minute)},
	}
	for _, l := range logs {
		if err := db.Create(&l).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
	}

	msgs, _ := json.Marshal([]map[string]any{{"role": "user", "content": "hi"}})
	traces := []TracePayloadRow{
		{ID: 1, RequestID: "req-1", SessionID: "sess-claude", Provider: "openai", ModelRequested: "default", Stream: false, AgentType: "claude-code", StatusCode: 200, NMessages: 1, Messages: string(msgs), RequestRaw: `{"model":"default"}`, ResponseRaw: "data: {...}", CreatedAt: base},
		{ID: 2, RequestID: "req-2", SessionID: "sess-claude", Provider: "openai", ModelRequested: "default", Stream: false, AgentType: "claude-code", StatusCode: 200, NMessages: 2, Messages: string(msgs), RequestRaw: `{"model":"default"}`, ResponseRaw: "data: {...}", CreatedAt: base.Add(time.Minute)},
		{ID: 3, RequestID: "req-3", SessionID: "sess-cb-err", Provider: "openai", ModelRequested: "default", Stream: false, AgentType: "codebuddy", StatusCode: 502, NMessages: 1, Messages: string(msgs), RequestRaw: `{"model":"default"}`, ErrorRaw: "upstream 401", CreatedAt: base.Add(2 * time.Minute)},
		{ID: 4, RequestID: "req-4", SessionID: "sess-cb-ok", Provider: "openai", ModelRequested: "default", Stream: false, AgentType: "codebuddy", StatusCode: 200, NMessages: 1, Messages: string(msgs), RequestRaw: `{"model":"default"}`, ResponseRaw: "data: {...}", CreatedAt: base.Add(3 * time.Minute)},
		// Empty bodies row: simulates marshal failure / capture missed /
		// upstream 4xx before parse. Must surface as JSON null, not invalid
		// JSON (regression for desktop trace viewer bug).
		{ID: 5, RequestID: "req-5", SessionID: "sess-empty", Provider: "openai", ModelRequested: "default", Stream: false, AgentType: "claude-code", StatusCode: 400, NMessages: 0, Messages: "", RequestRaw: "", ErrorRaw: "bad request", CreatedAt: base.Add(4 * time.Minute)},
	}
	for _, tr := range traces {
		if err := db.Create(&tr).Error; err != nil {
			t.Fatalf("seed trace_payloads: %v", err)
		}
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOverview(t *testing.T) {
	db := openTestDB(t)
	seedTestData(t, db)
	repo := NewQueryRepo(db)

	agents, tot, err := repo.Overview(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if tot.RequestCount != 4 {
		t.Errorf("totals request_count = %d, want 4", tot.RequestCount)
	}
	if tot.TotalTokens != 95 {
		t.Errorf("totals total_tokens = %d, want 95", tot.TotalTokens)
	}
	if tot.ErrorCount != 1 {
		t.Errorf("totals error_count = %d, want 1", tot.ErrorCount)
	}
	byAgent := map[string]AgentUsage{}
	for _, a := range agents {
		byAgent[a.AgentType] = a
	}
	if a, ok := byAgent["claude-code"]; !ok || a.RequestCount != 2 {
		t.Errorf("claude-code usage = %+v, want 2 requests", a)
	}
	if a, ok := byAgent["codebuddy"]; !ok || a.RequestCount != 2 || a.ErrorCount != 1 {
		t.Errorf("codebuddy usage = %+v, want 2 requests / 1 error", a)
	}
}

func TestListSessions(t *testing.T) {
	db := openTestDB(t)
	seedTestData(t, db)
	repo := NewQueryRepo(db)

	// No filter: all three sessions, most-recent first (sess-cb-ok last seen).
	sessions, total, err := repo.ListSessions(context.Background(), SessionListFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 3 {
		t.Fatalf("session total = %d, want 3", total)
	}
	if sessions[0].SessionID != "sess-cb-ok" {
		t.Errorf("first session = %q, want sess-cb-ok (most recent)", sessions[0].SessionID)
	}
	byID := map[string]SessionSummary{}
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	if s := byID["sess-claude"]; s.RequestCount != 2 || s.AgentType != "claude-code" {
		t.Errorf("sess-claude = %+v, want 2 requests / claude-code", s)
	}
	if s := byID["sess-cb-err"]; !s.HasErrors {
		t.Errorf("sess-cb-err HasErrors = false, want true")
	}

	// Filter by agent: codebuddy only -> 2 sessions.
	cb, _, err := repo.ListSessions(context.Background(), SessionListFilter{AgentType: "codebuddy"}, 1, 50)
	if err != nil {
		t.Fatalf("ListSessions filtered: %v", err)
	}
	if len(cb) != 2 {
		t.Errorf("codebuddy sessions = %d, want 2", len(cb))
	}
}

func TestListRequestLogs(t *testing.T) {
	db := openTestDB(t)
	seedTestData(t, db)
	repo := NewQueryRepo(db)

	rows, total, err := repo.ListRequestLogs(context.Background(), RequestLogFilter{AgentType: "claude-code"}, 1, 50)
	if err != nil {
		t.Fatalf("ListRequestLogs: %v", err)
	}
	if total != 2 {
		t.Fatalf("filtered total = %d, want 2", total)
	}
	if len(rows) != 2 || rows[0].RequestID != "req-2" {
		t.Errorf("rows = %+v, want req-2 first (DESC by created_at)", rows)
	}

	// Error filter.
	errRows, _, err := repo.ListRequestLogs(context.Background(), RequestLogFilter{ErrorType: "upstream_error"}, 1, 50)
	if err != nil {
		t.Fatalf("ListRequestLogs error filter: %v", err)
	}
	if len(errRows) != 1 || errRows[0].RequestID != "req-3" {
		t.Errorf("error rows = %+v, want req-3", errRows)
	}
}

func TestTraceQueries(t *testing.T) {
	db := openTestDB(t)
	seedTestData(t, db)
	repo := NewQueryRepo(db)

	// Session timeline (ASC).
	tl, err := repo.ListTraceBySession(context.Background(), "sess-claude", 50)
	if err != nil {
		t.Fatalf("ListTraceBySession: %v", err)
	}
	if len(tl) != 2 || tl[0].RequestID != "req-1" || tl[1].RequestID != "req-2" {
		t.Errorf("timeline = %+v, want req-1 then req-2", tl)
	}

	// Detail by row id.
	d, ok, err := repo.GetTraceByRowID(context.Background(), 3)
	if err != nil {
		t.Fatalf("GetTraceByRowID: %v", err)
	}
	if !ok || d.SessionID != "sess-cb-err" || d.StatusCode != 502 {
		t.Fatalf("detail = %+v ok=%v, want sess-cb-err/502", d, ok)
	}
	if string(d.Messages) == "" || d.ErrorRaw != "upstream 401" {
		t.Errorf("detail bodies = %+v, want messages + error_raw", d)
	}

	// Missing row.
	if _, ok, _ := repo.GetTraceByRowID(context.Background(), 999); ok {
		t.Error("GetTraceByRowID(999) ok = true, want false")
	}

	// Detail by request id.
	d2, ok2, err := repo.GetTraceByRequestID(context.Background(), "req-4")
	if err != nil || !ok2 || d2.SessionID != "sess-cb-ok" {
		t.Fatalf("GetTraceByRequestID = %+v ok=%v err=%v", d2, ok2, err)
	}

	// Regression: empty messages/request_raw columns must surface as JSON null,
	// not invalid JSON that breaks the desktop trace viewer
	// ("Unexpected end of JSON input").
	d3, ok3, err := repo.GetTraceByRowID(context.Background(), 5)
	if err != nil || !ok3 {
		t.Fatalf("GetTraceByRowID(5) = %+v ok=%v err=%v", d3, ok3, err)
	}
	if string(d3.Messages) != "null" {
		t.Errorf("empty messages = %q, want \"null\"", string(d3.Messages))
	}
	if string(d3.RequestRaw) != "null" {
		t.Errorf("empty request_raw = %q, want \"null\"", string(d3.RequestRaw))
	}
}
