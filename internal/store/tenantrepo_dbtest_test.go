//go:build dbtest

package store_test

import (
	"context"
	"testing"

	"voxeltoad/internal/store"
)

// scopedFixture seeds two tenants and returns their ids + a clean DB.
func scopedFixture(t *testing.T) (db *store.DB, tenantA, tenantB int64) {
	t.Helper()
	db = mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE api_keys, groups, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('tenant-a') RETURNING id`).Scan(&tenantA).Error; err != nil {
		t.Fatalf("seed tenant-a: %v", err)
	}
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('tenant-b') RETURNING id`).Scan(&tenantB).Error; err != nil {
		t.Fatalf("seed tenant-b: %v", err)
	}
	return db, tenantA, tenantB
}

// A tenant-scoped repo only ever sees its own tenant's rows: it injects the
// bound tenant_id into every query and offers no method to specify another
// (ADR-0017 §3, structural isolation).
func TestTenantRepo_ListIsScoped(t *testing.T) {
	ctx := context.Background()
	db, tenantA, tenantB := scopedFixture(t)

	repoA := store.NewTenantRepo(db, tenantA)
	repoB := store.NewTenantRepo(db, tenantB)

	if _, err := repoA.CreateGroup(ctx, "group-a1"); err != nil {
		t.Fatalf("A CreateGroup: %v", err)
	}
	if _, err := repoB.CreateGroup(ctx, "group-b1"); err != nil {
		t.Fatalf("B CreateGroup: %v", err)
	}

	groupsA, _, err := repoA.ListGroups(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groupsA) != 1 || groupsA[0].Name != "group-a1" {
		t.Errorf("repoA groups = %+v, want only group-a1", groupsA)
	}
	// A must not see B's group.
	for _, g := range groupsA {
		if g.Name == "group-b1" {
			t.Fatal("tenant A repo leaked tenant B's group")
		}
	}
}

func TestTenantRepo_APIKeyScoped(t *testing.T) {
	ctx := context.Background()
	db, tenantA, tenantB := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)
	repoB := store.NewTenantRepo(db, tenantB)

	if err := repoA.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "key_a", Hash: "h_a", AllowedModels: []string{"chat"}}); err != nil {
		t.Fatalf("A CreateAPIKey: %v", err)
	}
	if err := repoB.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "key_b", Hash: "h_b"}); err != nil {
		t.Fatalf("B CreateAPIKey: %v", err)
	}

	keysA, _, err := repoA.ListAPIKeys(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(keysA) != 1 || keysA[0].KeyID != "key_a" {
		t.Errorf("repoA keys = %+v, want only key_a", keysA)
	}

	// Revoke is scoped: A cannot revoke B's key (no rows affected within A's scope).
	revoked, err := repoA.RevokeAPIKey(ctx, "key_b")
	if err != nil {
		t.Fatalf("A RevokeAPIKey(key_b): %v", err)
	}
	if revoked {
		t.Error("tenant A revoked tenant B's key; scope leak")
	}
	// B's key is still active (visible to B, not revoked).
	keysB, _, _ := repoB.ListAPIKeys(ctx, "", 0)
	if len(keysB) != 1 || keysB[0].KeyID != "key_b" {
		t.Errorf("repoB keys = %+v, want key_b intact", keysB)
	}

	// A revoking its own key works.
	revoked, err = repoA.RevokeAPIKey(ctx, "key_a")
	if err != nil || !revoked {
		t.Errorf("A RevokeAPIKey(key_a) = %v, %v; want true,nil", revoked, err)
	}
}

// CreateGroup binds the constructor's tenant; a group created via repoA is owned
// by tenant A (verified by the DB row's tenant_id).
func TestTenantRepo_CreateBindsTenant(t *testing.T) {
	ctx := context.Background()
	db, tenantA, _ := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)

	id, err := repoA.CreateGroup(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	var gotTenant int64
	if err := db.Raw(`SELECT tenant_id FROM groups WHERE id = ?`, id).Scan(&gotTenant).Error; err != nil {
		t.Fatal(err)
	}
	if gotTenant != tenantA {
		t.Errorf("group tenant_id = %d, want %d (bound at construction)", gotTenant, tenantA)
	}
}

// A newly created tenant is enabled by default, and ListTenants reports it.
func TestListTenants_ReportsEnabled(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	if _, err := store.CreateTenant(ctx, db, "acme-enabled"); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	list, _, err := store.ListTenants(ctx, db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.TenantInfo
	for i := range list {
		if list[i].Name == "acme-enabled" {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatal("acme-enabled not found in ListTenants")
	}
	if !found.Enabled {
		t.Errorf("Enabled = false, want true (default) for a freshly created tenant")
	}
}

// ListTenants keyset-paginates over id, mirroring OperatorRepo.List: a limit
// smaller than the total returns a non-empty next cursor, and following it
// returns the remainder with an empty next cursor.
func TestListTenants_Pagination(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	for _, name := range []string{"page-a", "page-b", "page-c"} {
		if _, err := store.CreateTenant(ctx, db, name); err != nil {
			t.Fatalf("CreateTenant(%s): %v", name, err)
		}
	}

	page1, next1, err := store.ListTenants(ctx, db, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if next1 == "" {
		t.Fatal("page1 next cursor is empty, want non-empty (more rows remain)")
	}
	if page1[0].Name != "page-a" || page1[1].Name != "page-b" {
		t.Errorf("page1 = %+v, want [page-a page-b] in id order", page1)
	}

	page2, next2, err := store.ListTenants(ctx, db, next1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].Name != "page-c" {
		t.Errorf("page2 = %+v, want [page-c]", page2)
	}
	if next2 != "" {
		t.Errorf("page2 next cursor = %q, want empty (last page)", next2)
	}
}

// SetTenantEnabled toggles the enabled column and reports whether a row was
// affected (false for an unknown tenant name).
func TestSetTenantEnabled(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	if _, err := store.CreateTenant(ctx, db, "acme-toggle"); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	ok, err := store.SetTenantEnabled(ctx, db, "acme-toggle", false)
	if err != nil {
		t.Fatalf("SetTenantEnabled(disable): %v", err)
	}
	if !ok {
		t.Fatal("SetTenantEnabled(disable) = false, want true (row exists)")
	}

	list, _, err := store.ListTenants(ctx, db, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.TenantInfo
	for i := range list {
		if list[i].Name == "acme-toggle" {
			found = &list[i]
		}
	}
	if found == nil || found.Enabled {
		t.Errorf("after disable, tenant = %+v, want Enabled=false", found)
	}

	// Idempotent: disabling again still reports a matched row, not an error.
	ok, err = store.SetTenantEnabled(ctx, db, "acme-toggle", false)
	if err != nil || !ok {
		t.Errorf("repeated disable = %v, %v; want true, nil", ok, err)
	}

	// Re-enable flips it back.
	ok, err = store.SetTenantEnabled(ctx, db, "acme-toggle", true)
	if err != nil || !ok {
		t.Fatalf("SetTenantEnabled(enable): %v, %v", ok, err)
	}
	list, _, _ = store.ListTenants(ctx, db, "", 0)
	for _, ti := range list {
		if ti.Name == "acme-toggle" && !ti.Enabled {
			t.Error("after re-enable, tenant still shows Enabled=false")
		}
	}

	// Unknown tenant name: no row affected.
	ok, err = store.SetTenantEnabled(ctx, db, "does-not-exist", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("SetTenantEnabled on unknown tenant = true, want false")
	}
}

// --- ADR-0051: API key Application binding and environment ---

func TestTenantRepo_CreateAPIKeyWithBinding(t *testing.T) {
	ctx := context.Background()
	db, tenantA, _ := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)

	// Tenant A needs a group and an application.
	groupAID, err := createGroupRaw(db, tenantA, "group-bind")
	if err != nil {
		t.Fatal(err)
	}
	if err := createApplicationRaw(db, tenantA, "app-bind", groupAID); err != nil {
		t.Fatal(err)
	}
	var appID int64
	if err := db.Raw(`SELECT id FROM applications WHERE tenant_id = ? AND name = 'app-bind'`, tenantA).Scan(&appID).Error; err != nil {
		t.Fatal(err)
	}

	err = repoA.CreateAPIKey(ctx, store.APIKeySpec{
		KeyID:         "key_bind",
		Hash:          "hash_bind",
		GroupID:       &groupAID,
		ApplicationID: &appID,
		Environment:   "prod",
		AllowedModels: []string{"gpt-4o"},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey with binding: %v", err)
	}

	keys, _, err := repoA.ListAPIKeys(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(keys))
	}
	if keys[0].ApplicationID == nil || *keys[0].ApplicationID != appID {
		t.Errorf("ApplicationID = %v, want %d", keys[0].ApplicationID, appID)
	}
	if keys[0].Environment != "prod" {
		t.Errorf("Environment = %q, want prod", keys[0].Environment)
	}
}

func TestTenantRepo_SetAPIKeyApplication_BindUnbind(t *testing.T) {
	ctx := context.Background()
	db, tenantA, _ := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)

	groupAID, err := createGroupRaw(db, tenantA, "group-bu")
	if err != nil {
		t.Fatal(err)
	}
	if err := createApplicationRaw(db, tenantA, "app-bu", groupAID); err != nil {
		t.Fatal(err)
	}
	var appID int64
	if err := db.Raw(`SELECT id FROM applications WHERE tenant_id = ? AND name = 'app-bu'`, tenantA).Scan(&appID).Error; err != nil {
		t.Fatal(err)
	}

	if err := repoA.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "key_bu", Hash: "hash_bu", GroupID: &groupAID}); err != nil {
		t.Fatal(err)
	}

	// Bind.
	ok, err := repoA.SetAPIKeyApplication(ctx, "key_bu", &appID)
	if err != nil || !ok {
		t.Fatalf("SetAPIKeyApplication(bind): ok=%v err=%v", ok, err)
	}
	keys, _, _ := repoA.ListAPIKeys(ctx, "", 0)
	if len(keys) != 1 || keys[0].ApplicationID == nil || *keys[0].ApplicationID != appID {
		t.Errorf("after bind, ApplicationID = %v, want %d", keys[0].ApplicationID, appID)
	}

	// Unbind.
	ok, err = repoA.SetAPIKeyApplication(ctx, "key_bu", nil)
	if err != nil || !ok {
		t.Fatalf("SetAPIKeyApplication(unbind): ok=%v err=%v", ok, err)
	}
	keys, _, _ = repoA.ListAPIKeys(ctx, "", 0)
	if len(keys) != 1 || keys[0].ApplicationID != nil {
		t.Errorf("after unbind, ApplicationID = %v, want nil", keys[0].ApplicationID)
	}
}

func TestTenantRepo_SetAPIKeyEnvironment(t *testing.T) {
	ctx := context.Background()
	db, tenantA, _ := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)

	groupAID, err := createGroupRaw(db, tenantA, "group-env")
	if err != nil {
		t.Fatal(err)
	}
	if err := repoA.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "key_env", Hash: "hash_env", GroupID: &groupAID}); err != nil {
		t.Fatal(err)
	}

	ok, err := repoA.SetAPIKeyEnvironment(ctx, "key_env", "staging")
	if err != nil || !ok {
		t.Fatalf("SetAPIKeyEnvironment: ok=%v err=%v", ok, err)
	}
	keys, _, _ := repoA.ListAPIKeys(ctx, "", 0)
	if len(keys) != 1 || keys[0].Environment != "staging" {
		t.Errorf("Environment = %q, want staging", keys[0].Environment)
	}
}

func TestTenantRepo_SetAPIKeyApplication_CrossTenantBlocked(t *testing.T) {
	ctx := context.Background()
	db, tenantA, tenantB := scopedFixture(t)
	repoA := store.NewTenantRepo(db, tenantA)

	groupAID, err := createGroupRaw(db, tenantA, "group-cta")
	if err != nil {
		t.Fatal(err)
	}
	groupBID, err := createGroupRaw(db, tenantB, "group-ctb")
	if err != nil {
		t.Fatal(err)
	}
	// Application belongs to tenant B.
	if err := createApplicationRaw(db, tenantB, "app-ctb", groupBID); err != nil {
		t.Fatal(err)
	}
	var appBID int64
	if err := db.Raw(`SELECT id FROM applications WHERE tenant_id = ? AND name = 'app-ctb'`, tenantB).Scan(&appBID).Error; err != nil {
		t.Fatal(err)
	}

	if err := repoA.CreateAPIKey(ctx, store.APIKeySpec{KeyID: "key_ct", Hash: "hash_ct", GroupID: &groupAID}); err != nil {
		t.Fatal(err)
	}

	// Attempt to bind tenant A's key to tenant B's application.
	ok, err := repoA.SetAPIKeyApplication(ctx, "key_ct", &appBID)
	if err != nil {
		t.Fatalf("SetAPIKeyApplication cross-tenant: %v", err)
	}
	if ok {
		t.Error("ok = true, want false (cross-tenant application should not be bindable)")
	}
}
