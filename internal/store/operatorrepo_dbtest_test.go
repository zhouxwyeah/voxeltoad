//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

func freshOperatorDB(t *testing.T) *store.DB {
	t.Helper()
	db := mustMigratedDB(t)
	// Truncate tenants too (CASCADE), so a tenant seeded by another operator
	// test in this shared package/DB can't collide with this test's inserts.
	if err := db.Exec(`TRUNCATE operators, sessions, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return db
}

func TestOperatorRepo_CreateGetCount(t *testing.T) {
	ctx := context.Background()
	db := freshOperatorDB(t)
	repo := store.NewOperatorRepo(db)

	// super-admin (no tenant).
	sa, err := repo.Create(ctx, "root@x", "hash1", operator.RoleSuperAdmin, nil)
	if err != nil {
		t.Fatalf("create super-admin: %v", err)
	}
	if sa.ID == 0 || sa.Role != operator.RoleSuperAdmin || sa.TenantID != nil {
		t.Errorf("super-admin = %+v, want id>0, role super-admin, nil tenant", sa)
	}

	// GetByEmail returns it with the stored password hash for verification.
	got, hash, ok, err := repo.GetByEmail(ctx, "root@x")
	if err != nil || !ok {
		t.Fatalf("GetByEmail = ok %v err %v", ok, err)
	}
	if got.ID != sa.ID || hash != "hash1" {
		t.Errorf("GetByEmail = %+v hash %q, want id %d hash1", got, hash, sa.ID)
	}

	// Unknown email → ok=false.
	if _, _, ok, err := repo.GetByEmail(ctx, "ghost@x"); err != nil || ok {
		t.Errorf("GetByEmail(ghost) = ok %v err %v, want false,nil", ok, err)
	}

	// CountByRole reflects the one super-admin.
	n, err := repo.CountByRole(ctx, operator.RoleSuperAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CountByRole(super-admin) = %d, want 1", n)
	}
}

func TestOperatorRepo_TenantAdmin(t *testing.T) {
	ctx := context.Background()
	db := freshOperatorDB(t)
	repo := store.NewOperatorRepo(db)

	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	ta, err := repo.Create(ctx, "admin@acme", "h", operator.RoleTenantAdmin, &tenantID)
	if err != nil {
		t.Fatalf("create tenant-admin: %v", err)
	}
	if ta.TenantID == nil || *ta.TenantID != tenantID {
		t.Errorf("tenant-admin TenantID = %v, want %d", ta.TenantID, tenantID)
	}
}

func TestSessionRepo_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db := freshOperatorDB(t)
	opRepo := store.NewOperatorRepo(db)
	sessRepo := store.NewSessionRepo(db)

	op, err := opRepo.Create(ctx, "root@x", "h", operator.RoleSuperAdmin, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a session and look it up → resolves the operator.
	if err := sessRepo.Create(ctx, "tok-123", op.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, ok, err := sessRepo.Lookup(ctx, "tok-123")
	if err != nil || !ok {
		t.Fatalf("Lookup = ok %v err %v", ok, err)
	}
	if got.ID != op.ID || got.Role != operator.RoleSuperAdmin {
		t.Errorf("session operator = %+v, want id %d super-admin", got, op.ID)
	}

	// Revoke → lookup misses.
	if err := sessRepo.Delete(ctx, "tok-123"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, ok, _ := sessRepo.Lookup(ctx, "tok-123"); ok {
		t.Error("revoked session still resolves")
	}
}

func TestSessionRepo_ExpiredIsInvalid(t *testing.T) {
	ctx := context.Background()
	db := freshOperatorDB(t)
	opRepo := store.NewOperatorRepo(db)
	sessRepo := store.NewSessionRepo(db)

	op, _ := opRepo.Create(ctx, "root@x", "h", operator.RoleSuperAdmin, nil)
	if err := sessRepo.Create(ctx, "tok-old", op.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := sessRepo.Lookup(ctx, "tok-old"); err != nil || ok {
		t.Errorf("expired session Lookup = ok %v err %v, want false", ok, err)
	}
}

func TestSessionRepo_DeleteByOperatorRevokesAll(t *testing.T) {
	ctx := context.Background()
	db := freshOperatorDB(t)
	opRepo := store.NewOperatorRepo(db)
	sessRepo := store.NewSessionRepo(db)

	op, _ := opRepo.Create(ctx, "root@x", "h", operator.RoleSuperAdmin, nil)
	_ = sessRepo.Create(ctx, "t1", op.ID, time.Now().Add(time.Hour))
	_ = sessRepo.Create(ctx, "t2", op.ID, time.Now().Add(time.Hour))

	if err := sessRepo.DeleteByOperator(ctx, op.ID); err != nil {
		t.Fatalf("DeleteByOperator: %v", err)
	}
	for _, tok := range []string{"t1", "t2"} {
		if _, ok, _ := sessRepo.Lookup(ctx, tok); ok {
			t.Errorf("session %s survived DeleteByOperator", tok)
		}
	}
}
