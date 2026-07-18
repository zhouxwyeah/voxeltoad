//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// super-admin /me returns role + null tenant_name.
func TestMe_SuperAdmin(t *testing.T) {
	h, _, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/me", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		ID         int64   `json:"id"`
		Email      string  `json:"email"`
		Role       string  `json:"role"`
		TenantID   *int64  `json:"tenant_id"`
		TenantName *string `json:"tenant_name"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if out.Role != "super-admin" {
		t.Errorf("role = %q, want super-admin", out.Role)
	}
	if out.TenantID != nil {
		t.Errorf("tenant_id = %v, want nil for super-admin", out.TenantID)
	}
	if out.TenantName != nil {
		t.Errorf("tenant_name = %v, want nil for super-admin", out.TenantName)
	}
}

// tenant-admin /me returns role + tenant_name (resolved server-side).
func TestMe_TenantAdmin(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/me", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		ID         int64   `json:"id"`
		Email      string  `json:"email"`
		Role       string  `json:"role"`
		TenantID   *int64  `json:"tenant_id"`
		TenantName *string `json:"tenant_name"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if out.Role != "tenant-admin" {
		t.Errorf("role = %q, want tenant-admin", out.Role)
	}
	if out.Email != "ta@acme" {
		t.Errorf("email = %q, want ta@acme", out.Email)
	}
	if out.TenantID == nil || *out.TenantID != tenantID {
		t.Errorf("tenant_id = %v, want %d", out.TenantID, tenantID)
	}
	if out.TenantName == nil || *out.TenantName != "acme" {
		t.Errorf("tenant_name = %v, want acme", out.TenantName)
	}
}

// /me requires authentication → 401.
func TestMe_RequiresAuth(t *testing.T) {
	h, _ := newAdmin(t)
	rr := doAuth(t, h, "", http.MethodGet, "/api/v1/me", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /me status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}

// Self-service password change: 204 → can log in with the new password.
func TestMe_ChangePassword(t *testing.T) {
	h, _, saTok := authedAdmin(t)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators/me/password", map[string]any{
		"password": "new-root-pass-789",
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("change password status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	// Old password no longer works.
	oldRR := do(t, h, http.MethodPost, "/auth/login", map[string]any{
		"email": "root@x", "password": "root-pass-123",
	})
	if oldRR.Code != http.StatusUnauthorized {
		t.Errorf("old password login status = %d, want 401; body=%s", oldRR.Code, oldRR.Body.String())
	}

	// New password works.
	if login(t, h, "root@x", "new-root-pass-789") == "" {
		t.Error("new password does not authenticate after change")
	}
}

// Empty password returns 400.
func TestMe_ChangePasswordEmpty(t *testing.T) {
	h, _, saTok := authedAdmin(t)

	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/operators/me/password", map[string]any{
		"password": "",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty password status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// Unauthenticated request returns 401.
func TestMe_ChangePasswordUnauthenticated(t *testing.T) {
	h, _ := newAdmin(t)

	rr := doAuth(t, h, "", http.MethodPost, "/api/v1/operators/me/password", map[string]any{
		"password": "any-password",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}
