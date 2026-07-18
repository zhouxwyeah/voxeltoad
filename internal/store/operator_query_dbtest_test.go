//go:build dbtest

package store_test

import (
	"context"
	"testing"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// freshOperatorRepo migrates + clears operators/sessions/tenants and returns a
// repo plus a seeded tenant id for tenant-admin rows.
func freshOperatorRepo(t *testing.T) (*store.OperatorRepo, *store.DB, int64) {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE operators, sessions, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('acme') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return store.NewOperatorRepo(db), db, tenantID
}

// List returns non-secret operator views (no password hash), keyset-paginated
// on id, across pages with no gaps or dupes.
func TestOperatorRepo_ListPagination(t *testing.T) {
	ctx := context.Background()
	repo, _, tenantID := freshOperatorRepo(t)

	if _, err := repo.Create(ctx, "root@x", "h", operator.RoleSuperAdmin, nil); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"a@acme", "b@acme", "c@acme"} {
		if _, err := repo.Create(ctx, e, "h", operator.RoleTenantAdmin, &tenantID); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		rows, next, err := repo.List(ctx, cursor, 2)
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Errorf("operator id %d returned twice", r.ID)
			}
			seen[r.ID] = true
			if r.Email == "" || r.Role == "" {
				t.Errorf("row missing non-secret fields: %+v", r)
			}
		}
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > 6 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 4 {
		t.Errorf("saw %d distinct operators, want 4", len(seen))
	}
}

// List exposes tenant_id for tenant-admins and leaves it nil for super-admins.
func TestOperatorRepo_ListRoleAndTenant(t *testing.T) {
	ctx := context.Background()
	repo, _, tenantID := freshOperatorRepo(t)
	if _, err := repo.Create(ctx, "root@x", "h", operator.RoleSuperAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "ta@acme", "h", operator.RoleTenantAdmin, &tenantID); err != nil {
		t.Fatal(err)
	}

	rows, _, err := repo.List(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	byEmail := map[string]store.OperatorInfo{}
	for _, r := range rows {
		byEmail[r.Email] = r
	}
	if sa := byEmail["root@x"]; sa.Role != string(operator.RoleSuperAdmin) || sa.TenantID != nil {
		t.Errorf("super-admin row = %+v, want role super-admin, nil tenant", sa)
	}
	if ta := byEmail["ta@acme"]; ta.Role != string(operator.RoleTenantAdmin) || ta.TenantID == nil || *ta.TenantID != tenantID {
		t.Errorf("tenant-admin row = %+v, want role tenant-admin, tenant %d", ta, tenantID)
	}
}

// Delete removes an operator and reports whether a row was hit.
func TestOperatorRepo_Delete(t *testing.T) {
	ctx := context.Background()
	repo, _, tenantID := freshOperatorRepo(t)
	op, err := repo.Create(ctx, "ta@acme", "h", operator.RoleTenantAdmin, &tenantID)
	if err != nil {
		t.Fatal(err)
	}

	hit, err := repo.Delete(ctx, op.ID)
	if err != nil || !hit {
		t.Fatalf("Delete existing = %v,%v; want true,nil", hit, err)
	}
	// Second delete is a miss.
	hit, err = repo.Delete(ctx, op.ID)
	if err != nil || hit {
		t.Fatalf("Delete missing = %v,%v; want false,nil", hit, err)
	}
	// Gone from List.
	rows, _, err := repo.List(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("after delete List = %v, want empty", rows)
	}
}

// Update changes email and/or password_hash by operator id. Missing id returns
// false. Duplicate email is a constraint error.
func TestOperatorRepo_Update(t *testing.T) {
	ctx := context.Background()
	repo, db, tenantID := freshOperatorRepo(t)

	t.Run("update email", func(t *testing.T) {
		op, err := repo.Create(ctx, "before@acme", "oldhash", operator.RoleTenantAdmin, &tenantID)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := repo.Update(ctx, op.ID, "after@acme", "", nil, nil)
		if err != nil {
			t.Fatalf("Update email: %v", err)
		}
		if !ok {
			t.Fatal("Update existing operator returned false")
		}
		updated, _, found, err := repo.GetByEmail(ctx, "after@acme")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("updated operator not found by new email")
		}
		if updated.Email != "after@acme" {
			t.Errorf("email = %q, want after@acme", updated.Email)
		}
		if updated.ID != op.ID {
			t.Errorf("id = %d, want %d", updated.ID, op.ID)
		}
		_, _, found, err = repo.GetByEmail(ctx, "before@acme")
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Error("old email still resolves")
		}
	})

	t.Run("update password only", func(t *testing.T) {
		op, err := repo.Create(ctx, "x@acme", "oldhash", operator.RoleTenantAdmin, &tenantID)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := repo.Update(ctx, op.ID, "", "newhash", nil, nil)
		if err != nil {
			t.Fatalf("Update password: %v", err)
		}
		if !ok {
			t.Fatal("Update existing operator returned false")
		}
		_, h, found, err := repo.GetByEmail(ctx, "x@acme")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("operator not found after password update")
		}
		if h != "newhash" {
			t.Errorf("password hash = %q, want newhash", h)
		}
	})

	t.Run("update tenant_id", func(t *testing.T) {
		var t2 int64
		if err := db.Raw(`INSERT INTO tenants (name) VALUES ('beta') RETURNING id`).Scan(&t2).Error; err != nil {
			t.Fatalf("seed second tenant: %v", err)
		}
		op, err := repo.Create(ctx, "y@acme", "h", operator.RoleTenantAdmin, &tenantID)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := repo.Update(ctx, op.ID, "", "", nil, &t2)
		if err != nil {
			t.Fatalf("Update tenant_id: %v", err)
		}
		if !ok {
			t.Fatal("Update existing operator returned false")
		}
		updated, _, found, err := repo.GetByEmail(ctx, "y@acme")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("operator not found after tenant_id update")
		}
		if updated.TenantID == nil || *updated.TenantID != t2 {
			t.Errorf("tenant_id = %v, want %d", updated.TenantID, t2)
		}
	})

	t.Run("missing id returns false", func(t *testing.T) {
		ok, err := repo.Update(ctx, 9999, "nobody@x", "", nil, nil)
		if err != nil {
			t.Fatalf("Update missing: %v", err)
		}
		if ok {
			t.Error("Update missing operator returned true")
		}
	})

	t.Run("duplicate email is a constraint error", func(t *testing.T) {
		if _, err := repo.Create(ctx, "dup@acme", "h", operator.RoleTenantAdmin, &tenantID); err != nil {
			t.Fatal(err)
		}
		op, err := repo.Create(ctx, "other@acme", "h", operator.RoleTenantAdmin, &tenantID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.Update(ctx, op.ID, "dup@acme", "", nil, nil)
		if err == nil {
			t.Fatal("expected constraint error, got nil")
		}
	})
}
