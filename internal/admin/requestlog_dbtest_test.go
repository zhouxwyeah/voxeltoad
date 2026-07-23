//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"voxeltoad/internal/store"
)

// seedRequestLogRow inserts a request_logs row for a tenant (created_at
// defaults to now()).
func seedRequestLogRow(t *testing.T, db *store.DB, tenant, provider, errorType string) {
	seedRequestLogRowWithAgent(t, db, tenant, provider, errorType, "")
}

// seedRequestLogRowWithAgent is seedRequestLogRow with an explicit agent_type.
// Use this when the test needs to verify per-agent aggregation (e.g. overview
// AgentStats). Empty agentType matches the legacy default (”).
func seedRequestLogRowWithAgent(t *testing.T, db *store.DB, tenant, provider, errorType, agentType string) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO request_logs
		   (tenant, group_name, api_key_id, provider,
		    model_requested, model_resolved, stream,
		    prompt_tokens, completion_tokens, total_tokens,
		    ttft_ms, duration_ms, error_type, blocked_by, fallback,
		    request_id, session_id, agent_type)
		 VALUES (?, '', 'k', ?, 'chat', 'gpt-4o', false, 10, 20, 30, 50, 100, ?, '', false, 'req-1', 'sess-1', ?)`,
		tenant, provider, errorType, agentType,
	).Error; err != nil {
		t.Fatalf("seed request_logs: %v", err)
	}
}

// super-admin sees all tenants' request logs via the envelope.
func TestRequestLogs_SuperAdminSeesAll(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")
	seedRequestLogRow(t, db, "other", "openai", "")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("request-logs status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows, total := decodePage(t, rr)
	if len(rows) != 2 {
		t.Errorf("super-admin request-log rows = %d, want 2 (all tenants)", len(rows))
	}
	if total != 2 {
		t.Errorf("super-admin request-log total = %d, want 2", total)
	}
}

// A tenant-admin sees only its own tenant's request logs — never another
// tenant's (structural isolation, ADR-0019/0021 pattern).
func TestRequestLogs_TenantAdminScoped(t *testing.T) {
	h, db, _ := authedAdmin(t)

	var acmeID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&acmeID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	seedRequestLogRow(t, db, "acme", "openai", "")
	seedRequestLogRow(t, db, "other", "openai", "")

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/request-logs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("request-logs status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows, total := decodePage(t, rr)
	if len(rows) != 1 {
		t.Fatalf("tenant-admin request-log rows = %d, want 1 (own tenant only)", len(rows))
	}
	if total != 1 {
		t.Errorf("tenant-admin request-log total = %d, want 1 (must not count other tenant)", total)
	}
	if rows[0]["tenant"] != "acme" {
		t.Errorf("tenant-admin saw tenant %v, want acme (cross-tenant leak)", rows[0]["tenant"])
	}
}

// A provider filter narrows the feed.
func TestRequestLogs_FilterByProvider(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")
	seedRequestLogRow(t, db, "acme", "claude", "")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?provider=openai", nil)
	rows, _ := decodePage(t, rr)
	for _, r := range rows {
		if r["provider"] != "openai" {
			t.Errorf("filtered request-logs returned provider=%v, want openai", r["provider"])
		}
	}
	if len(rows) != 1 {
		t.Errorf("provider-filtered rows = %d, want 1", len(rows))
	}
}

// Request-log reads are never audited (ADR-0017 §5, same as usage/audit).
func TestRequestLogs_ReadsNotAudited(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")

	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs", nil)

	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs`).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 0 {
		t.Errorf("audit rows = %d, want 0 (reads must not be audited)", count)
	}
}

// P1 advanced filters: tenant, group_name, api_key_id, stream, fallback.
func TestRequestLogs_AdvancedFilters(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed tenants so the tenant filter's existence check passes.
	for _, name := range []string{"acme", "other"} {
		if err := db.Exec(`INSERT INTO tenants (name) VALUES (?) ON CONFLICT DO NOTHING`, name).Error; err != nil {
			t.Fatalf("seed tenant %s: %v", name, err)
		}
	}

	// Seed rows with distinct values for each filter dimension.
	seed := func(tenant, group, keyID, provider string, stream, fallback bool, requestID string) {
		t.Helper()
		if err := db.Exec(
			`INSERT INTO request_logs
			   (tenant, group_name, api_key_id, provider,
			    model_requested, model_resolved, stream,
			    prompt_tokens, completion_tokens, total_tokens,
			    ttft_ms, duration_ms, error_type, blocked_by, fallback,
			    request_id, session_id)
			 VALUES (?, ?, ?, ?, 'chat', 'gpt-4o', ?, 10, 20, 30, 50, 100, '', '', ?, ?, 'sess-1')`,
			tenant, group, keyID, provider, stream, fallback, requestID,
		).Error; err != nil {
			t.Fatalf("seed request_logs: %v", err)
		}
	}
	seed("acme", "team-a", "k1", "openai", true, false, "req-1")
	seed("acme", "team-b", "k2", "openai", false, true, "req-2")
	seed("other", "team-a", "k1", "claude", false, false, "req-3")

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"tenant filter", "tenant=acme", 2},
		{"group_name filter", "group_name=team-a", 2},
		{"api_key_id filter", "api_key_id=k1", 2},
		{"stream=true", "stream=true", 1},
		{"stream=false", "stream=false", 2},
		{"fallback=true", "fallback=true", 1},
		{"fallback=false", "fallback=false", 2},
		{"combined tenant+stream", "tenant=acme&stream=true", 1},
		{"combined tenant+fallback", "tenant=acme&fallback=false", 1},
		{"request_id filter", "request_id=req-2", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs?"+tc.query, nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
			}
			rows, _ := decodePage(t, rr)
			if len(rows) != tc.want {
				t.Errorf("rows = %d, want %d; body=%s", len(rows), tc.want, rr.Body.String())
			}
		})
	}
}

// Tenant-admin cannot widen its scope via the tenant filter (structural).
func TestRequestLogs_TenantAdminTenantFilterIgnored(t *testing.T) {
	h, db, _ := authedAdmin(t)

	var acmeID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&acmeID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	seedRequestLogRow(t, db, "acme", "openai", "")
	seedRequestLogRow(t, db, "other", "openai", "")

	// tenant-admin passes tenant=other — must still see only its own rows.
	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/request-logs?tenant=other", nil)
	rows, _ := decodePage(t, rr)
	if len(rows) != 1 || rows[0]["tenant"] != "acme" {
		t.Errorf("tenant-admin tenant filter leaked: rows=%v", rows)
	}
}

// The session-trace endpoint returns the session's request timeline + cost
// summary. Guards against a route-registration regression (a 404 here means the
// route was not wired under /api/v1/request-logs/sessions/:session_id).
func TestRequestLogs_SessionTrace(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedRequestLogRow(t, db, "acme", "openai", "")

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/request-logs/sessions/sess-1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("session-trace status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var trace struct {
		SessionID string `json:"session_id"`
		Requests  []any  `json:"requests"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &trace); err != nil {
		t.Fatalf("unmarshal trace: %v; body=%s", err, rr.Body.String())
	}
	if trace.SessionID != "sess-1" {
		t.Errorf("session_id = %q, want sess-1", trace.SessionID)
	}
	if len(trace.Requests) != 1 {
		t.Errorf("requests = %d, want 1", len(trace.Requests))
	}
}
