//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// tenant-admin creates a group within its own tenant and lists it.
func TestGroupCRUD_TenantAdminCreateList(t *testing.T) {
	h, db, saTok := authedAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")
	_ = saTok

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create group status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/groups", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list groups status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	list := decodeList(t, rr)
	if len(list) != 1 || list[0]["name"] != "team-a" {
		t.Errorf("groups = %v, want one team-a", list)
	}
	if enabled, ok := list[0]["enabled"].(bool); !ok || !enabled {
		t.Errorf("group enabled = %v, want true (default)", list[0]["enabled"])
	}
}

// GET /api/v1/groups respects ?cursor/?limit and returns a non-empty
// next_cursor when more rows remain (mirrors tenants list pagination).
func TestGroupCRUD_ListPagination(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	for _, name := range []string{"g-a", "g-b", "g-c"} {
		if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": name}); rr.Code != http.StatusCreated {
			t.Fatalf("create group %s: %d %s", name, rr.Code, rr.Body.String())
		}
	}

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/groups?limit=2", nil)
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

	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/groups?limit=2&cursor="+env.NextCursor, nil)
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

// Duplicate group name → 400 (unique-constraint translated to client error).
func TestGroupCRUD_DuplicateNameRejected(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "dup"}); rr.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", rr.Code, rr.Body.String())
	}
	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "dup"})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate name status = %d, want 4xx; body=%s", rr.Code, rr.Body.String())
	}
}

// PATCH toggle enabled (reversible) — mirrors tenant pattern, audited.
func TestGroupCRUD_ToggleEnabled(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"}); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	// Disable.
	rr := doAuth(t, h, taTok, http.MethodPatch, "/api/v1/groups/team-a", map[string]any{"enabled": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rr.Code, rr.Body.String())
	}
	var enabled bool
	if err := db.Raw(`SELECT enabled FROM groups WHERE name = 'team-a' AND tenant_id = ?`, tenantID).Scan(&enabled).Error; err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("group still enabled after PATCH {enabled:false}")
	}

	// Re-enable.
	rr = doAuth(t, h, taTok, http.MethodPatch, "/api/v1/groups/team-a", map[string]any{"enabled": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", rr.Code, rr.Body.String())
	}
	if err := db.Raw(`SELECT enabled FROM groups WHERE name = 'team-a' AND tenant_id = ?`, tenantID).Scan(&enabled).Error; err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("group still disabled after PATCH {enabled:true}")
	}

	// Two mutations audited.
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'update' AND resource_type = 'group' AND resource_id = 'team-a'`,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("audit rows for group PATCH = %d, want 2 (disable + re-enable)", count)
	}
}

// PATCH unknown group → 404.
func TestGroupCRUD_UnknownGroup404(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	rr := doAuth(t, h, taTok, http.MethodPatch, "/api/v1/groups/ghost", map[string]any{"enabled": false})
	if rr.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown group status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// DELETE without API key refs → 204.
func TestGroupCRUD_DeleteSucceeds(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"}); rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}

	rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/groups/team-a", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	// Group is gone.
	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/groups", nil)
	list := decodeList(t, rr)
	if len(list) != 0 {
		t.Errorf("after delete, groups = %d, want 0", len(list))
	}

	// Audited as delete.
	var count int64
	if err := db.Raw(
		`SELECT count(*) FROM audit_logs WHERE action = 'delete' AND resource_type = 'group' AND resource_id = 'team-a'`,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit delete rows = %d, want 1", count)
	}
}

// DELETE when API keys reference the group → 409 (mirrors provider/model).
func TestGroupCRUD_DeleteWithRefs409(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// Create group.
	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"}); rr.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}
	var groupID int64
	if err := db.Raw(`SELECT id FROM groups WHERE name = 'team-a' AND tenant_id = ?`, tenantID).Scan(&groupID).Error; err != nil {
		t.Fatal(err)
	}

	// Create an API key referencing the group via raw insert (the create handler
	// doesn't accept group_id yet, but the store layer supports it).
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, allowed_models)
		 VALUES ('ref-key', 'DEADBEEF' || repeat('0', 56), ?, ?, '[]')`,
		tenantID, groupID,
	).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}

	rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/groups/team-a", nil)
	if rr.Code != http.StatusConflict {
		t.Errorf("delete with refs status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	// Group is still there.
	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/groups", nil)
	list := decodeList(t, rr)
	found := false
	for _, g := range list {
		if g["name"] == "team-a" {
			found = true
			break
		}
	}
	if !found {
		t.Error("group should still exist after rejected delete")
	}
}

// super-admin cannot access tenant-scoped groups (requireTenantAdmin rejects).
func TestGroupCRUD_RejectsSuperAdmin(t *testing.T) {
	h, db, tok := authedAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	_ = tenantID

	rr := doAuth(t, h, tok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "x"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("super-admin create group status = %d, want 403", rr.Code)
	}
	rr = doAuth(t, h, tok, http.MethodGet, "/api/v1/groups", nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("super-admin list groups status = %d, want 403", rr.Code)
	}
}

// tenant-admin from tenant A cannot manipulate tenant B's groups.
func TestGroupCRUD_CrossTenantIsolation(t *testing.T) {
	h, db := newAdmin(t)

	var aID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&aID).Error; err != nil {
		t.Fatalf("seed tenant a: %v", err)
	}
	var bID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('beta') RETURNING id`).Scan(&bID).Error; err != nil {
		t.Fatalf("seed tenant b: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", aID)
	seedTenantAdmin(t, db, "ta@beta", "ta-pass-456", bID)

	// acme admin creates a group in acme.
	taAcmeTok := login(t, h, "ta@acme", "ta-pass-123")
	if rr := doAuth(t, h, taAcmeTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "acme-team"}); rr.Code != http.StatusCreated {
		t.Fatalf("create acme group: %d %s", rr.Code, rr.Body.String())
	}

	// beta admin lists — should see only beta groups (empty).
	taBetaTok := login(t, h, "ta@beta", "ta-pass-456")
	rr := doAuth(t, h, taBetaTok, http.MethodGet, "/api/v1/groups", nil)
	list := decodeList(t, rr)
	if len(list) != 0 {
		t.Errorf("beta admin sees %d groups, want 0 (acme group not visible)", len(list))
	}

	// beta admin tries to PATCH acme's group → 404 (not in tenant).
	rr = doAuth(t, h, taBetaTok, http.MethodPatch, "/api/v1/groups/acme-team", map[string]any{"enabled": false})
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant PATCH status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}
