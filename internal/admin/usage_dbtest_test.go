//go:build dbtest

package admin_test

import (
	"net/http"
	"testing"

	"voxeltoad/internal/store"
)

// seedUsageRow inserts a usage record for a tenant (created_at defaults to now()).
func seedUsageRow(t *testing.T, db *store.DB, tenant, provider string, cost int64) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO usage_records
		   (tenant, group_name, api_key_id, provider, model, prompt_tokens, completion_tokens, cost)
		 VALUES (?, '', 'k', ?, 'gpt-4o', 10, 20, ?)`,
		tenant, provider, cost,
	).Error; err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

// super-admin sees all tenants' usage via the envelope.
func TestUsage_SuperAdminSeesAll(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 2 {
		t.Errorf("super-admin usage rows = %d, want 2 (all tenants)", len(rows))
	}
}

// A tenant-admin sees only its own tenant's usage — never another tenant's.
func TestUsage_TenantAdminScoped(t *testing.T) {
	h, db, _ := authedAdmin(t)

	var acmeID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&acmeID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 999)

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/usage", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Fatalf("tenant-admin usage rows = %d, want 1 (own tenant only)", len(rows))
	}
	if rows[0]["tenant"] != "acme" {
		t.Errorf("tenant-admin saw tenant %v, want acme (cross-tenant leak)", rows[0]["tenant"])
	}
}

// Summary aggregates by the requested dimension.
func TestUsageSummary_GroupByProvider(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "acme", "openai", 200)
	seedUsageRow(t, db, "acme", "claude", 50)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary?group_by=provider", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	byKey := map[string]map[string]any{}
	for _, r := range rows {
		byKey[r["group_key"].(string)] = r
	}
	// JSON numbers decode as float64.
	if got := byKey["openai"]["cost"].(float64); got != 300 {
		t.Errorf("openai cost = %v, want 300", got)
	}
	if got := byKey["claude"]["cost"].(float64); got != 50 {
		t.Errorf("claude cost = %v, want 50", got)
	}
}

// An unknown group_by dimension is rejected 400.
func TestUsageSummary_RejectsUnknownDimension(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary?group_by=bogus", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown group_by status = %d, want 400", rr.Code)
	}
}

// Usage reads are never audited (ADR-0017 §5).
func TestUsage_ReadsNotAudited(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)

	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/usage", nil)
	_ = doAuth(t, h, tok, http.MethodGet, "/api/v1/usage/summary", nil)

	var count int64
	if err := db.Raw(`SELECT count(*) FROM audit_logs`).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 0 {
		t.Errorf("audit rows = %d, want 0 (reads must not be audited)", count)
	}
}

// super-admin can scope usage to a single tenant via ?tenant=NAME.
func TestUsage_SuperAdminFilterByTenant(t *testing.T) {
	h, db, tok := authedAdmin(t)
	if err := db.Exec(`INSERT INTO tenants (name) VALUES ('acme')`).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "other", "openai", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?tenant=acme", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Errorf("filtered rows = %d, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0]["tenant"] != "acme" {
		t.Errorf("saw tenant %v, want acme", rows[0]["tenant"])
	}
}

// An unknown tenant filter returns 400.
func TestUsage_UnknownTenantFilter(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?tenant=nonexistent", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown tenant status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// provider query param filters usage rows.
func TestUsage_ProviderFilter(t *testing.T) {
	h, db, tok := authedAdmin(t)
	seedUsageRow(t, db, "acme", "openai", 100)
	seedUsageRow(t, db, "acme", "claude", 200)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/usage?provider=claude", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows := decodeList(t, rr)
	if len(rows) != 1 {
		t.Errorf("filtered rows = %d, want 1", len(rows))
	}
	if len(rows) > 0 && rows[0]["provider"] != "claude" {
		t.Errorf("saw provider %v, want claude", rows[0]["provider"])
	}
}
