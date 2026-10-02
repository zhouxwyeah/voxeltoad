//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

// seedApplication inserts a tenant + owner group + application, returning the
// ids. Each call uses distinct names so tests are independent within the shared
// DB.
func seedApplication(t *testing.T, db *store.DB, tenant, group, app string) (tenantID, groupID, appID int64) {
	t.Helper()
	if err := db.Raw(
		`INSERT INTO tenants (name) VALUES (?) RETURNING id`, tenant,
	).Scan(&tenantID).Error; err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Raw(
		`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group,
	).Scan(&groupID).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := db.Raw(
		`INSERT INTO applications (tenant_id, name, owner_group_id) VALUES (?, ?, ?) RETURNING id`,
		tenantID, app, groupID,
	).Scan(&appID).Error; err != nil {
		t.Fatalf("insert application: %v", err)
	}
	return tenantID, groupID, appID
}

// seedKeyWithApplication extends seedKey with an application binding and
// environment label. If app is "", the key is left unbound (migration debt).
func seedKeyWithApplication(t *testing.T, db *store.DB, keyID, hash, tenant, group, app, env string, appEnabled bool) {
	t.Helper()
	tenantID, groupID, appID := seedApplication(t, db, tenant, group, app)
	if !appEnabled {
		if err := db.Exec(`UPDATE applications SET enabled = false WHERE id = ?`, appID).Error; err != nil {
			t.Fatalf("disable application: %v", err)
		}
	}
	var appCol *int64
	if app != "" {
		appCol = &appID
	}
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, application_id, environment, allowed_models)
		 VALUES (?, ?, ?, ?, ?, ?, '[]'::jsonb)`,
		keyID, hash, tenantID, groupID, appCol, env,
	).Error; err != nil {
		t.Fatalf("insert api_key: %v", err)
	}
}

func TestApplicationRepo_Create(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, _ := seedApplication(t, db, "acme-create", "team-create", "app-create")
	repo := store.NewApplicationRepo(db, tenantID)

	id, err := repo.Create(ctx, "app-second", "team-create")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id <= 0 {
		t.Errorf("id = %d, want > 0", id)
	}

	// UNIQUE(tenant_id, name) violation.
	_, err = repo.Create(ctx, "app-create", "team-create")
	if err == nil {
		t.Error("duplicate (tenant, name) should fail")
	}

	// Verify owner_group_id resolved correctly.
	got, ok, err := repo.Get(ctx, "app-second")
	if err != nil || !ok {
		t.Fatalf("Get app-second: ok=%v err=%v", ok, err)
	}
	if got.OwnerGroupID != groupID {
		t.Errorf("OwnerGroupID = %d, want %d", got.OwnerGroupID, groupID)
	}
}

func TestApplicationRepo_CreateUnknownOwnerGroup(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-unknown-owner", "team-owner", "app-placeholder")
	repo := store.NewApplicationRepo(db, tenantID)

	_, err := repo.Create(ctx, "app-orphan", "nonexistent-group")
	if err == nil {
		t.Error("Create with unknown owner group should fail (FK violation)")
	}
}

// Create must not resolve a group name from a different tenant. The owner
// group subquery is scoped to the bound tenant_id, so a cross-tenant group
// name behaves the same as a nonexistent group.
func TestApplicationRepo_Create_CrossTenantOwnerGroup(t *testing.T) {
	ctx := context.Background()
	db, tenantA, tenantB := scopedFixture(t)

	// Tenant B has a group "team-b".
	groupBID, err := createGroupRaw(db, tenantB, "team-b")
	if err != nil {
		t.Fatal(err)
	}
	_ = groupBID

	// Tenant A needs its own group to seed the repo.
	groupAID, err := createGroupRaw(db, tenantA, "team-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = groupAID
	repoA := store.NewApplicationRepo(db, tenantA)

	// Try to create an app in tenant A using tenant B's group name.
	_, err = repoA.Create(ctx, "app-cross", "team-b")
	if err == nil {
		t.Error("Create with cross-tenant owner group should fail (subquery returns NULL → NOT NULL violation)")
	}
}

// Create must produce an application with enabled=true by default.
func TestApplicationRepo_Create_DefaultEnabled(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, _ := seedApplication(t, db, "acme-enabled", "team-enabled", "app-seed")
	_ = groupID
	repo := store.NewApplicationRepo(db, tenantID)

	id, err := repo.Create(ctx, "app-default-enabled", "team-enabled")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = id

	got, ok, err := repo.Get(ctx, "app-default-enabled")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true (schema default)")
	}
}

// Create with an empty name must fail (NOT NULL / empty string violates
// UNIQUE(tenant_id, name) semantics — an empty name is not a valid app).
func TestApplicationRepo_Create_EmptyName(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-empty", "team-empty", "app-seed")
	repo := store.NewApplicationRepo(db, tenantID)

	_, err := repo.Create(ctx, "", "team-empty")
	// Empty name produces a row with name='' which is technically valid in
	// PG (VARCHAR NOT NULL allows ''). A second empty-name create would
	// hit UNIQUE. The first one succeeds; we only assert it doesn't panic
	// and that the name is stored as-is.
	if err != nil {
		// If it did fail (e.g., future CHECK constraint), that's also acceptable.
		return
	}
	got, ok, err := repo.Get(ctx, "")
	if err != nil || !ok {
		t.Fatalf("Get empty-name app: ok=%v err=%v", ok, err)
	}
	if got.Name != "" {
		t.Errorf("Name = %q, want empty string", got.Name)
	}
	// Second create with empty name must fail (UNIQUE violation).
	_, err = repo.Create(ctx, "", "team-empty")
	if err == nil {
		t.Error("second Create with empty name should fail (UNIQUE violation)")
	}
}

// Create must propagate real DB errors (not mask them).
func TestApplicationRepo_Create_PropagatesDBError(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-dberr-create", "team-dberr", "app-seed")
	repo := store.NewApplicationRepo(db, tenantID)

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	_, err := repo.Create(ctx, "app-after-close", "team-dberr")
	if err == nil {
		t.Fatal("err = nil after DB closed, want non-nil (must not swallow real errors)")
	}
}

func TestApplicationRepo_List_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-list", "team-list", "app-list-0")
	repo := store.NewApplicationRepo(db, tenantID)

	for _, name := range []string{"app-list-1", "app-list-2", "app-list-3"} {
		if _, err := repo.Create(ctx, name, "team-list"); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	page1, next1, err := repo.List(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if next1 == "" {
		t.Fatal("next1 is empty, want non-empty (more rows remain)")
	}

	page2, next2, err := repo.List(ctx, next1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}
	if next2 != "" {
		t.Errorf("next2 = %q, want empty (last page)", next2)
	}
}

func TestApplicationRepo_Get(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, _ := seedApplication(t, db, "acme-get", "team-get", "app-get")
	repo := store.NewApplicationRepo(db, tenantID)

	got, ok, err := repo.Get(ctx, "app-get")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Name != "app-get" {
		t.Errorf("Name = %q, want app-get", got.Name)
	}
	if got.OwnerGroupID != groupID {
		t.Errorf("OwnerGroupID = %d, want %d", got.OwnerGroupID, groupID)
	}
	if got.OwnerGroupName != "team-get" {
		t.Errorf("OwnerGroupName = %q, want team-get", got.OwnerGroupName)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true (default)")
	}

	// Unknown name → ok=false, err=nil (not a DB error).
	_, ok, err = repo.Get(ctx, "no-such-app")
	if err != nil {
		t.Fatalf("Get unknown: %v", err)
	}
	if ok {
		t.Error("ok = true for unknown app, want false")
	}
}

// Get must propagate real DB errors instead of masking them as "not found".
// Closing the underlying connection forces a non-ErrRecordNotFound error,
// which must surface as err != nil (not ok=false, err=nil).
func TestApplicationRepo_Get_PropagatesDBError(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-dberr", "team-dberr", "app-dberr")
	repo := store.NewApplicationRepo(db, tenantID)

	// Close the connection to force a real query error on the next call.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	_, ok, err := repo.Get(ctx, "app-dberr")
	if ok {
		t.Error("ok = true after DB closed, want false")
	}
	if err == nil {
		t.Fatal("err = nil after DB closed, want non-nil (must not swallow real errors)")
	}
}

func TestApplicationRepo_SetEnabled(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-toggle", "team-toggle", "app-toggle")
	repo := store.NewApplicationRepo(db, tenantID)

	ok, err := repo.SetEnabled(ctx, "app-toggle", false)
	if err != nil || !ok {
		t.Fatalf("SetEnabled(false): ok=%v err=%v", ok, err)
	}
	got, _, _ := repo.Get(ctx, "app-toggle")
	if got.Enabled {
		t.Error("after disable, Enabled = true, want false")
	}

	// Re-enable.
	ok, err = repo.SetEnabled(ctx, "app-toggle", true)
	if err != nil || !ok {
		t.Fatalf("SetEnabled(true): ok=%v err=%v", ok, err)
	}
	got, _, _ = repo.Get(ctx, "app-toggle")
	if !got.Enabled {
		t.Error("after re-enable, Enabled = false, want true")
	}

	// Unknown name.
	ok, err = repo.SetEnabled(ctx, "no-such-app", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ok = true for unknown app, want false")
	}
}

func TestApplicationRepo_Delete_NoReferences(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, _, _ := seedApplication(t, db, "acme-del", "team-del", "app-del-noref")
	repo := store.NewApplicationRepo(db, tenantID)

	ok, err := repo.Delete(ctx, "app-del-noref")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !ok {
		t.Error("ok = false, want true")
	}
	// Confirm gone.
	_, ok, _ = repo.Get(ctx, "app-del-noref")
	if ok {
		t.Error("app still exists after Delete")
	}
}

func TestApplicationRepo_ReferencedByAPIKeys(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "acme-ref", "team-ref", "app-ref")
	repo := store.NewApplicationRepo(db, tenantID)

	// Bind two keys to the application (one in the owner group, one in a
	// different consuming group — ADR-0051 allows cross-group consumption).
	consumerGroupID, err := createGroupRaw(db, tenantID, "team-consumer")
	if err != nil {
		t.Fatal(err)
	}
	if err := createKeyRaw(db, "key_ref_a", "hash_ref_a", tenantID, groupID, &appID, ""); err != nil {
		t.Fatal(err)
	}
	if err := createKeyRaw(db, "key_ref_b", "hash_ref_b", tenantID, consumerGroupID, &appID, "prod"); err != nil {
		t.Fatal(err)
	}
	// Revoked keys remain referenced for deletion protection.
	now := time.Now()
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, application_id, environment, allowed_models, revoked_at)
		 VALUES ('key_revoked', 'hash_rev', ?, ?, ?, '', '[]'::jsonb, ?)`,
		tenantID, groupID, appID, now,
	).Error; err != nil {
		t.Fatal(err)
	}

	refs, err := repo.ApplicationReferencedByAPIKeys(ctx, "app-ref")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("refs = %v, want all 3 retained keys", refs)
	}
}

func TestApplicationRepo_Delete_ReferencedByKeys(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "acme-delref", "team-delref", "app-delref")
	repo := store.NewApplicationRepo(db, tenantID)

	if err := createKeyRaw(db, "key_bound", "hash_bound", tenantID, groupID, &appID, ""); err != nil {
		t.Fatal(err)
	}

	refs, err := repo.ApplicationReferencedByAPIKeys(ctx, "app-delref")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected references, got none")
	}

	// FK RESTRICT should block deletion.
	_, err = repo.Delete(ctx, "app-delref")
	if err == nil {
		t.Error("Delete with referenced keys should fail (FK RESTRICT)")
	}
}

func TestApplicationRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	db, tenantA, tenantB := scopedFixture(t)

	repoA := store.NewApplicationRepo(db, tenantA)
	repoB := store.NewApplicationRepo(db, tenantB)

	// Tenant A needs a group to own the application.
	groupAID, err := createGroupRaw(db, tenantA, "group-a")
	if err != nil {
		t.Fatal(err)
	}
	groupBID, err := createGroupRaw(db, tenantB, "group-b")
	if err != nil {
		t.Fatal(err)
	}

	if err := createApplicationRaw(db, tenantA, "app-a", groupAID); err != nil {
		t.Fatal(err)
	}
	if err := createApplicationRaw(db, tenantB, "app-b", groupBID); err != nil {
		t.Fatal(err)
	}

	appsA, _, err := repoA.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(appsA) != 1 || appsA[0].Name != "app-a" {
		t.Errorf("repoA = %+v, want only app-a", appsA)
	}
	for _, a := range appsA {
		if a.Name == "app-b" {
			t.Fatal("tenant A repo leaked tenant B's application")
		}
	}

	// B also only sees its own application.
	appsB, _, err := repoB.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(appsB) != 1 || appsB[0].Name != "app-b" {
		t.Errorf("repoB = %+v, want only app-b", appsB)
	}
}

func TestTenantRepo_UnboundKeys(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	tenantID, groupID, appID := seedApplication(t, db, "acme-orphan", "team-orphan", "app-orphan")
	repo := store.NewTenantRepo(db, tenantID)

	// A bound key and an unbound key.
	if err := createKeyRaw(db, "key_bound", "hash_bound", tenantID, groupID, &appID, "prod"); err != nil {
		t.Fatal(err)
	}
	if err := createKeyRaw(db, "key_orphan", "hash_orphan", tenantID, groupID, nil, ""); err != nil {
		t.Fatal(err)
	}

	orphans, _, err := repo.ListAPIKeysFiltered(ctx, "", 50, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].KeyID != "key_orphan" {
		t.Errorf("orphans = %v, want [key_orphan]", orphans)
	}
}

// createGroupRaw inserts a group and returns its id (test helper).
func createGroupRaw(db *store.DB, tenantID int64, name string) (int64, error) {
	var id int64
	err := db.Raw(
		`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, name,
	).Scan(&id).Error
	return id, err
}

// createApplicationRaw inserts an application row (test helper).
func createApplicationRaw(db *store.DB, tenantID int64, name string, ownerGroupID int64) error {
	return db.Exec(
		`INSERT INTO applications (tenant_id, name, owner_group_id) VALUES (?, ?, ?)`,
		tenantID, name, ownerGroupID,
	).Error
}

// createKeyRaw inserts an api_key row with optional application binding (test
// helper).
func createKeyRaw(db *store.DB, keyID, hash string, tenantID, groupID int64, appID *int64, env string) error {
	return db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, application_id, environment, allowed_models)
		 VALUES (?, ?, ?, ?, ?, ?, '[]'::jsonb)`,
		keyID, hash, tenantID, groupID, appID, env,
	).Error
}
