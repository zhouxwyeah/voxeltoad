//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// super-admin can read any scope's balance.
func TestQuotaRead_SuperAdminAnyScope(t *testing.T) {
	h, db, tok := authedAdmin(t)
	// Seed tenant so tenant:acme scope validation passes (required by
	// validateQuotaScope for tenant:-prefixed scopes).
	if err := db.Exec(`INSERT INTO tenants (name) VALUES ('acme')`).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 500, "currency": "usd",
	}); rr.Code != http.StatusOK {
		t.Fatalf("topup: %d %s", rr.Code, rr.Body.String())
	}

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/quotas?scope=tenant:acme", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Balance int64 `json:"balance"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Balance != 500 {
		t.Errorf("balance = %d, want 500", out.Balance)
	}
}

// A tenant-admin may read its own tenant's scope but not another tenant's.
func TestQuotaRead_TenantAdminScoped(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}
	var acmeID int64
	if err := db.Raw(`SELECT id FROM tenants WHERE name='acme'`).Scan(&acmeID).Error; err != nil {
		t.Fatal(err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", acmeID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// Seed balances for both acme and another tenant.
	_ = doAuth(t, h, saTok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 100, "currency": "usd",
	})
	_ = doAuth(t, h, saTok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:other", "delta": 999, "currency": "usd",
	})

	// Own tenant → 200.
	if rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/quotas?scope=tenant:acme", nil); rr.Code != http.StatusOK {
		t.Errorf("own-tenant read status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Another tenant → 403.
	if rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/quotas?scope=tenant:other", nil); rr.Code != http.StatusForbidden {
		t.Errorf("cross-tenant read status = %d, want 403", rr.Code)
	}
}

// Missing scope → 400.
func TestQuotaRead_RequiresScope(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/quotas", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no-scope read status = %d, want 400", rr.Code)
	}
}
