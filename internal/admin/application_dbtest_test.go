//go:build dbtest

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// tenant-admin creates an application within its own tenant and lists it.
func TestApplicationCRUD_TenantAdminCreateList(t *testing.T) {
	h, db, _ := authedAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// Create owner group first.
	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"}); rr.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}

	// Create application.
	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "app-a", "owner_group": "team-a"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create application status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["name"] != "app-a" || created["enabled"] != true {
		t.Errorf("created = %v, want name=app-a enabled=true", created)
	}

	// List.
	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/applications", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	list := decodeList(t, rr)
	if len(list) != 1 || list[0]["name"] != "app-a" {
		t.Errorf("list = %v, want one app-a", list)
	}
	if list[0]["owner_group_name"] != "team-a" {
		t.Errorf("owner_group_name = %v, want team-a", list[0]["owner_group_name"])
	}
}

func TestApplicationCRUD_ListPagination(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	// Create owner group.
	if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"}); rr.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}

	for _, name := range []string{"app-1", "app-2", "app-3"} {
		if rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": name, "owner_group": "team-a"}); rr.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rr.Code, rr.Body.String())
		}
	}

	rr := doAuth(t, h, taTok, http.MethodGet, "/api/v1/applications?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Data) != 2 || env.NextCursor == "" {
		t.Fatalf("page1 len=%d next=%q, want 2 + non-empty", len(env.Data), env.NextCursor)
	}

	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/applications?limit=2&cursor="+env.NextCursor, nil)
	json.Unmarshal(rr.Body.Bytes(), &env)
	if len(env.Data) != 1 || env.NextCursor != "" {
		t.Errorf("page2 len=%d next=%q, want 1 + empty", len(env.Data), env.NextCursor)
	}
}

func TestApplicationCRUD_DuplicateNameRejected(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "dup", "owner_group": "team-a"})

	rr := doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "dup", "owner_group": "team-a"})
	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("duplicate name status = %d, want 4xx; body=%s", rr.Code, rr.Body.String())
	}
}

func TestApplicationCRUD_ToggleEnabled(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "app-a", "owner_group": "team-a"})

	// Disable.
	rr := doAuth(t, h, taTok, http.MethodPatch, "/api/v1/applications/app-a", map[string]any{"enabled": false})
	if rr.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched["id"] == nil || patched["owner_group_id"] == nil || patched["owner_group_name"] != "team-a" || patched["name"] != "app-a" || patched["enabled"] != false {
		t.Fatalf("PATCH must return a complete Application: %v", patched)
	}
	var enabled bool
	db.Raw(`SELECT enabled FROM applications WHERE name = 'app-a' AND tenant_id = ?`, tenantID).Scan(&enabled)
	if enabled {
		t.Error("app still enabled after PATCH {enabled:false}")
	}

	// Re-enable.
	rr = doAuth(t, h, taTok, http.MethodPatch, "/api/v1/applications/app-a", map[string]any{"enabled": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", rr.Code, rr.Body.String())
	}

	// Two mutations audited.
	var count int64
	db.Raw(`SELECT count(*) FROM audit_logs WHERE action = 'update' AND resource_type = 'application' AND resource_id = 'app-a'`).Scan(&count)
	if count != 2 {
		t.Errorf("audit rows = %d, want 2", count)
	}
}

func TestApplicationCRUD_UnknownApp404(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID)
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	rr := doAuth(t, h, taTok, http.MethodPatch, "/api/v1/applications/ghost", map[string]any{"enabled": false})
	if rr.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	rr = doAuth(t, h, taTok, http.MethodDelete, "/api/v1/applications/ghost", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestApplicationCRUD_DeleteSucceeds(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID)
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "app-a", "owner_group": "team-a"})

	rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/applications/app-a", nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	// Gone.
	rr = doAuth(t, h, taTok, http.MethodGet, "/api/v1/applications", nil)
	list := decodeList(t, rr)
	if len(list) != 0 {
		t.Errorf("after delete, list = %d, want 0", len(list))
	}

	// Audited.
	var count int64
	db.Raw(`SELECT count(*) FROM audit_logs WHERE action = 'delete' AND resource_type = 'application' AND resource_id = 'app-a'`).Scan(&count)
	if count != 1 {
		t.Errorf("audit delete rows = %d, want 1", count)
	}
}

func TestApplicationCRUD_DeleteWithRefs409(t *testing.T) {
	h, db := newAdmin(t)

	var tenantID int64
	db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID)
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", tenantID)
	taTok := login(t, h, "ta@acme", "ta-pass-123")

	doAuth(t, h, taTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	doAuth(t, h, taTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "app-a", "owner_group": "team-a"})

	// Seed a key bound to the application.
	var groupID, appID int64
	db.Raw(`SELECT id FROM groups WHERE name = 'team-a' AND tenant_id = ?`, tenantID).Scan(&groupID)
	db.Raw(`SELECT id FROM applications WHERE name = 'app-a' AND tenant_id = ?`, tenantID).Scan(&appID)
	db.Exec(`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, application_id, environment, allowed_models)
		VALUES ('ref-key', 'DEADBEEF' || repeat('0', 56), ?, ?, ?, '', '[]'::jsonb)`, tenantID, groupID, appID)

	rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/applications/app-a", nil)
	if rr.Code != http.StatusConflict {
		t.Errorf("delete with refs status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if rr := doAuth(t, h, taTok, http.MethodDelete, "/api/v1/api-keys/ref-key", nil); rr.Code != http.StatusNoContent {
		t.Fatalf("revoke referenced key: %d %s", rr.Code, rr.Body.String())
	}
	rr = doAuth(t, h, taTok, http.MethodDelete, "/api/v1/applications/app-a", nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("retained revoked key must return409, not FK500: %d %s", rr.Code, rr.Body.String())
	}
}

func TestApplicationCRUD_CrossTenantIsolation(t *testing.T) {
	h, db := newAdmin(t)

	var aID, bID int64
	db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&aID)
	db.Raw(`INSERT INTO tenants (name) VALUES ('beta') RETURNING id`).Scan(&bID)
	seedTenantAdmin(t, db, "ta@acme", "ta-pass-123", aID)
	seedTenantAdmin(t, db, "ta@beta", "ta-pass-456", bID)

	taAcmeTok := login(t, h, "ta@acme", "ta-pass-123")
	doAuth(t, h, taAcmeTok, http.MethodPost, "/api/v1/groups", map[string]any{"name": "team-a"})
	doAuth(t, h, taAcmeTok, http.MethodPost, "/api/v1/applications", map[string]any{"name": "acme-app", "owner_group": "team-a"})

	// beta admin sees no applications.
	taBetaTok := login(t, h, "ta@beta", "ta-pass-456")
	rr := doAuth(t, h, taBetaTok, http.MethodGet, "/api/v1/applications", nil)
	list := decodeList(t, rr)
	if len(list) != 0 {
		t.Errorf("beta admin sees %d applications, want 0", len(list))
	}

	// beta admin cannot PATCH acme's application.
	rr = doAuth(t, h, taBetaTok, http.MethodPatch, "/api/v1/applications/acme-app", map[string]any{"enabled": false})
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant PATCH status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}
