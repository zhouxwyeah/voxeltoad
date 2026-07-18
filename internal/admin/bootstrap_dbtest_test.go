//go:build dbtest

package admin_test

import (
	"context"
	"testing"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// Bootstrap creates the first super-admin iff none exists; it is idempotent
// (ADR-0017 §4).
func TestBootstrap_CreatesFirstSuperAdminOnce(t *testing.T) {
	ctx := context.Background()
	_, db := newAdmin(t)

	created, err := admin.Bootstrap(ctx, db, "root@x", "bootstrap-pass-1")
	if err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	if !created {
		t.Error("first Bootstrap should report created=true")
	}

	// The operator exists and the password verifies.
	op, hash, ok, err := store.NewOperatorRepo(db).GetByEmail(ctx, "root@x")
	if err != nil || !ok {
		t.Fatalf("GetByEmail = ok %v err %v", ok, err)
	}
	if op.Role != operator.RoleSuperAdmin || op.TenantID != nil {
		t.Errorf("bootstrapped operator = %+v, want super-admin/nil tenant", op)
	}
	if valid, _ := operator.VerifyPassword("bootstrap-pass-1", hash); !valid {
		t.Error("bootstrapped password does not verify")
	}

	// Second call is a no-op: a super-admin already exists.
	created, err = admin.Bootstrap(ctx, db, "other@x", "another-pass")
	if err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	if created {
		t.Error("second Bootstrap should be a no-op (created=false)")
	}
	n, _ := store.NewOperatorRepo(db).CountByRole(ctx, operator.RoleSuperAdmin)
	if n != 1 {
		t.Errorf("super-admin count = %d, want 1 (idempotent)", n)
	}
}
