package admin

import (
	"context"
	"fmt"

	"voxeltoad/internal/operator"
	"voxeltoad/internal/store"
)

// Bootstrap creates the first super-admin operator iff no super-admin exists
// (ADR-0017 §4). It is idempotent: if a super-admin is already present it makes
// no change and returns created=false. This is the only way the first operator
// is created — no seed migration, no default account, no env credentials.
func Bootstrap(ctx context.Context, db *store.DB, email, password string) (created bool, err error) {
	if email == "" || password == "" {
		return false, fmt.Errorf("admin: bootstrap requires email and password")
	}
	repo := store.NewOperatorRepo(db)

	n, err := repo.CountByRole(ctx, operator.RoleSuperAdmin)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil // a super-admin already exists — no-op
	}

	hash, err := operator.HashPassword(password)
	if err != nil {
		return false, err
	}
	if _, err := repo.Create(ctx, email, hash, operator.RoleSuperAdmin, nil); err != nil {
		return false, err
	}
	return true, nil
}
