//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// super-admin can top up a quota scope; the endpoint returns the new balance
// and a repeat top-up increments (atomic add, not overwrite). The mutation is
// audited as resource_type "quota".
func TestQuotaTopUp_SuperAdmin(t *testing.T) {
	h, db, tok := authedAdmin(t)

	// Seed the tenant so tenant:acme scope validation passes.
	var acmeID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&acmeID).Error; err != nil {
		t.Fatalf("seed tenant acme: %v", err)
	}
	_ = acmeID

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 500, "currency": "usd",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("topup status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Scope   string `json:"scope"`
		Balance int64  `json:"balance"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Scope != "tenant:acme" || out.Balance != 500 {
		t.Errorf("topup response = %+v, want scope tenant:acme balance 500", out)
	}

	// Second top-up increments (does not overwrite).
	rr = doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 250, "currency": "usd",
	})
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Balance != 750 {
		t.Errorf("balance after second topup = %d, want 750 (500+250)", out.Balance)
	}

	// The mutation was audited.
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'create' AND resource_type = 'quota' AND resource_id = 'tenant:acme'`,
	).Scan(&count).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if count != 2 {
		t.Errorf("quota audit rows = %d, want 2 (two top-ups audited)", count)
	}
}

// A non-positive delta is rejected at the write boundary (400).
func TestQuotaTopUp_RejectsNonPositiveDelta(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 0, "currency": "usd",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("zero-delta topup status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// A tenant-admin may not top up quotas (super-admin only) → 403.
func TestQuotaTopUp_RejectsTenantAdmin(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	tok := login(t, h, "ta@acme", "ta-pass-123")

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:acme", "delta": 100, "currency": "usd",
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin topup status = %d, want 403", rr.Code)
	}
}

// scope validation: tenant:不存在 → 400
func TestQuotaTopUp_RejectsNonexistentTenant(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "tenant:nonexistent", "delta": 500, "currency": "usd",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("nonexistent tenant scope status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// scope validation: key:任意 still works (pre-funding, no existence check).
func TestQuotaTopUp_KeyScopePreFunding(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "key:sk-future-key", "delta": 500, "currency": "usd",
	})
	if rr.Code != http.StatusOK {
		t.Errorf("key scope prefund status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// scope validation: bare string still works (free-form quota).
func TestQuotaTopUp_BareScope(t *testing.T) {
	h, _, tok := authedAdmin(t)
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/quotas/topup", map[string]any{
		"scope": "my-custom-quota", "delta": 500, "currency": "usd",
	})
	if rr.Code != http.StatusOK {
		t.Errorf("bare scope status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
