//go:build dbtest

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// super-admin can create and list tenants (global tenancy management, ADR-0017).
func TestTenantManagement_SuperAdmin(t *testing.T) {
	h, _, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/tenants", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list tenants status = %d, want 200", rr.Code)
	}
	list := decodeList(t, rr)
	if len(list) != 1 || list[0]["name"] != "acme" {
		t.Errorf("tenants = %v, want one acme", list)
	}
}

// GET /api/v1/tenants respects ?cursor/?limit and returns a non-empty
// next_cursor when more rows remain, empty on the last page (mirrors
// operators' pagination, TestOperators_List).
func TestTenantManagement_ListPagination(t *testing.T) {
	h, _, tok := authedAdmin(t)

	for _, name := range []string{"page-a", "page-b", "page-c"} {
		if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": name}); rr.Code != http.StatusCreated {
			t.Fatalf("create tenant %s: %d %s", name, rr.Code, rr.Body.String())
		}
	}

	rr := doAuth(t, h, tok, http.MethodGet, "/api/v1/tenants?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(env.Data) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(env.Data))
	}
	if env.NextCursor == "" {
		t.Fatal("page1 next_cursor is empty, want non-empty (more rows remain)")
	}

	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/tenants?limit=2&cursor="+env.NextCursor, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list page2 status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env2 struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env2); err != nil {
		t.Fatalf("decode page2: %v; body=%s", err, rr.Body.String())
	}
	if len(env2.Data) != 1 {
		t.Fatalf("page2 len = %d, want 1", len(env2.Data))
	}
	if env2.NextCursor != "" {
		t.Errorf("page2 next_cursor = %q, want empty (last page)", env2.NextCursor)
	}
}

// Creating a tenant with a name that already exists is a client error (400),
// not a 500 from the unique-constraint violation (mirrors
// TestOperators_DuplicateEmail).
func TestTenantManagement_DuplicateNameRejected(t *testing.T) {
	h, _, tok := authedAdmin(t)

	if rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "dup"}); rr.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "dup"})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate name status = %d, want 4xx; body=%s", rr.Code, rr.Body.String())
	}
}

// A tenant-admin issues an API key within its own tenant; the plaintext is
// returned once and only the hash is stored (ADR-0006).
func TestAPIKeyIssuance_TenantAdmin(t *testing.T) {
	ctx := context.Background()
	h, db, saTok := authedAdmin(t)

	// super-admin creates the tenant.
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", rr.Code, rr.Body.String())
	}
	var tenantID int64
	if err := db.Raw(`SELECT id FROM tenants WHERE name = 'acme'`).Scan(&tenantID).Error; err != nil {
		t.Fatal(err)
	}

	// Seed a tenant-admin for acme and log in.
	hash, _ := operator.HashPassword("ta-pass-123")
	if _, err := store.NewOperatorRepo(db).Create(ctx, "ta@acme", hash, operator.RoleTenantAdmin, &tenantID); err != nil {
		t.Fatalf("seed tenant-admin: %v", err)
	}
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// Issue an API key (no allowed_models — empty = all models allowed).
	rr = doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "key_acme_1",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("issue key status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		KeyID     string `json:"key_id"`
		Plaintext string `json:"api_key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Plaintext == "" {
		t.Error("plaintext api_key must be returned once at creation")
	}

	// The stored row is scoped to acme, and only the hash is persisted.
	var storedTenant int64
	var storedHash string
	if err := db.Raw(`SELECT tenant_id, hash FROM api_keys WHERE key_id = 'key_acme_1'`).
		Row().Scan(&storedTenant, &storedHash); err != nil {
		t.Fatalf("read key row: %v", err)
	}
	if storedTenant != tenantID {
		t.Errorf("key tenant_id = %d, want %d (bound to tenant-admin's tenant)", storedTenant, tenantID)
	}
	if storedHash == out.Plaintext || storedHash == "" {
		t.Error("stored value must be the hash, not the plaintext")
	}
}

// A super-admin cannot issue tenant-scoped API keys (it has no tenant); the
// tenant endpoints require tenant-admin.
func TestAPIKeyIssuance_RejectsSuperAdmin(t *testing.T) {
	h, _, saTok := authedAdmin(t)
	rr := doAuth(t, h, saTok, http.MethodPost, "/api/v1/api-keys", map[string]any{"key_id": "k"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("super-admin issuing tenant key status = %d, want 403", rr.Code)
	}
}

// super-admin can disable and re-enable a tenant (reversible, unlike api-key
// revocation): PATCH /api/v1/tenants/{name} {enabled} flips the flag.
func TestTenantEnabled_SuperAdminTogglesIt(t *testing.T) {
	h, db, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/tenants", map[string]any{"name": "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", rr.Code, rr.Body.String())
	}

	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/tenants/acme", map[string]any{"enabled": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("disable tenant status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var enabled bool
	if err := db.Raw(`SELECT enabled FROM tenants WHERE name = 'acme'`).Scan(&enabled).Error; err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("tenant still enabled after PATCH {enabled:false}")
	}

	// Re-enable flips it back — this is what makes it reversible, unlike
	// api_keys.revoked_at.
	rr = doAuth(t, h, tok, http.MethodPatch, "/api/v1/tenants/acme", map[string]any{"enabled": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enable tenant status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if err := db.Raw(`SELECT enabled FROM tenants WHERE name = 'acme'`).Scan(&enabled).Error; err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("tenant still disabled after PATCH {enabled:true}")
	}

	// The mutation is audited (resource_type=tenant, action=update).
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'update' AND resource_type = 'tenant' AND resource_id = 'acme'`,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("audit rows for tenant PATCH = %d, want 2 (disable + re-enable)", count)
	}
}

// tenant-admin cannot toggle tenant enablement — global tenancy management is
// super-admin only.
func TestTenantEnabled_RejectsTenantAdmin(t *testing.T) {
	h, db := newAdmin(t)
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	rr := doAuth(t, h, taTok, http.MethodPatch, "/api/v1/tenants/acme", map[string]any{"enabled": false})
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin PATCH tenant status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// PATCH on an unknown tenant name is a 404, not a silent no-op.
func TestTenantEnabled_UnknownTenant404(t *testing.T) {
	h, _, tok := authedAdmin(t)

	rr := doAuth(t, h, tok, http.MethodPatch, "/api/v1/tenants/ghost", map[string]any{"enabled": false})
	if rr.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown tenant status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// Creating a key with an allowed_models entry that doesn't match a known model
// alias is rejected (400). This keeps the allowed_models contract honest.
func TestAPIKeyIssuance_RejectsUnknownModel(t *testing.T) {
	h, db := newAdmin(t)
	ctx := context.Background()

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")
	_ = ctx

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"key_id": "k1", "allowed_models": []string{"ghost-model"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown model allowed_models status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
