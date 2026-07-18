//go:build dbtest

package admin_test

import (
	"net/http"
	"testing"
)

// The headline ADR-0019 property: a tenant-admin reading /audit sees operations
// against its tenant — INCLUDING a super-admin's action on it (e.g. a quota
// top-up) — but never another tenant's rows nor global config mutations.
func TestAudit_TenantAdminSeesSuperAdminActionsOnIt(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	// super-admin creates a tenant + a tenant-admin for it.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", rr.Code, rr.Body.String())
	}
	var acmeID int64
	if err := db.Raw(`SELECT id FROM tenants WHERE name='acme'`).Scan(&acmeID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// super-admin tops up acme's quota (attributed to acme) and creates a global
	// provider (attributed to no tenant).
	if rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 1000, "currency": "usd",
	}); rr.Code != http.StatusOK {
		t.Fatalf("topup: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", rr.Code, rr.Body.String())
	}

	// tenant-admin reads /audit: must see the acme quota top-up, NOT the global
	// provider create nor the tenant-create-of-another-tenant.
	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/audit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("audit read status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	rows, total := decodePage(t, rr)
	sawQuota := false
	for _, r := range rows {
		if r["tenant"] != "acme" {
			t.Errorf("tenant-admin audit leaked row tenant=%v (want only acme)", r["tenant"])
		}
		if r["resource_type"] == "quota" {
			sawQuota = true
		}
		if r["resource_type"] == "provider" {
			t.Error("tenant-admin must not see global provider mutation")
		}
	}
	if !sawQuota {
		t.Error("tenant-admin should see the super-admin quota top-up on its tenant")
	}
	if int64(len(rows)) > total {
		t.Errorf("rows=%d exceeds reported total=%d", len(rows), total)
	}
}

// super-admin's /audit sees everything (global + all tenants).
func TestAudit_SuperAdminSeesAll(t *testing.T) {
	h, _, tok := authedAdmin(t)

	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/audit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want 200", rr.Code)
	}
	rows, _ := decodePage(t, rr)
	if len(rows) < 2 {
		t.Errorf("super-admin audit rows = %d, want >= 2 (tenant create + provider create)", len(rows))
	}
}

// A resource_type filter narrows the feed.
func TestAudit_FilterByResourceType(t *testing.T) {
	h, _, tok := authedAdmin(t)
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	_ = doAuth(t, h, tok, http.MethodPost, "/api/v1/providers", map[string]any{
		"name": "p1", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k",
	})

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/audit?resource_type=provider", nil)
	rows, _ := decodePage(t, rr)
	for _, r := range rows {
		if r["resource_type"] != "provider" {
			t.Errorf("filtered audit returned resource_type=%v, want provider", r["resource_type"])
		}
	}
	if len(rows) != 1 {
		t.Errorf("provider-filtered rows = %d, want 1", len(rows))
	}
}
