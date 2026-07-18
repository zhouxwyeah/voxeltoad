//go:build dbtest

package store_test

import (
	"testing"

	"voxeltoad/internal/store"
)

// TestMigrateOperatorsSessions asserts the RBAC tables exist with their
// load-bearing constraints (ADR-0017), including Phase-2 role_id FK.
func TestMigrateOperatorsSessions(t *testing.T) {
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE operators, sessions, tenants RESTART IDENTITY CASCADE`).Error; err != nil {
		t.Fatalf("truncate: %v", err)
	}

	for _, table := range []string{"operators", "sessions", "roles", "role_permissions"} {
		var exists bool
		if err := db.Raw(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema='public' AND table_name = ?)`, table,
		).Scan(&exists).Error; err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %q missing after migrate", table)
		}
	}

	// Seed a tenant for the tenant-admin FK.
	var tenantID int64
	if err := db.Raw(`INSERT INTO tenants (name) VALUES ('op-tenant') RETURNING id`).Scan(&tenantID).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// A valid super-admin (NULL tenant) inserts fine.
	if err := db.Exec(`
		INSERT INTO operators (email, password_hash, role, role_id, tenant_id)
		VALUES ('root@x', 'h', 'super-admin',
		        (SELECT id FROM roles WHERE name = 'super-admin'), NULL)`).Error; err != nil {
		t.Fatalf("insert super-admin: %v", err)
	}

	// email is UNIQUE.
	if err := db.Exec(`
		INSERT INTO operators (email, password_hash, role, role_id, tenant_id)
		VALUES ('root@x', 'h', 'super-admin',
		        (SELECT id FROM roles WHERE name = 'super-admin'), NULL)`).Error; err == nil {
		t.Error("duplicate operator email allowed; want UNIQUE violation")
	}

	// role check: NULL role_id is rejected by NOT NULL constraint (since
	// migration 00013 dropped the role CHECK). role text is denormalised and
	// any value is accepted as long as role_id is a valid FK.
	if err := db.Exec(`
		INSERT INTO operators (email, password_hash, role, role_id, tenant_id)
		VALUES ('bad@x', 'h', 'wizard', NULL, NULL)`).Error; err == nil {
		t.Error("NULL role_id allowed; want NOT NULL violation")
	}

	// Custom roles (non-builtin) work now because the hardcoded role CHECK
	// was dropped by migration 00013.
	customRoleID, err := seedCustomRole(t, db, "billing-viewer", "tenant")
	if err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO operators (email, password_hash, role, role_id, tenant_id)
		VALUES ('billing@x', 'h', 'billing-viewer', ?, ?)`,
		customRoleID, tenantID).Error; err != nil {
		t.Fatalf("insert operator with custom role: %v", err)
	}

	// The operator_role_tenant CHECK was also dropped in 00013.
	// Handler-level scope-kind validation replaces it; the DB allows any
	// role/tenant combination (safety enforced in code, not schema).
}

func seedCustomRole(t *testing.T, db *store.DB, name, scopeKind string) (int64, error) {
	t.Helper()
	var id int64
	err := db.Raw(
		`INSERT INTO roles (name, scope_kind, is_builtin, description)
		 VALUES (?, ?, false, 'custom test role')
		 ON CONFLICT (name) DO UPDATE SET scope_kind = excluded.scope_kind
		 RETURNING id`,
		name, scopeKind,
	).Scan(&id).Error
	if err != nil {
		return 0, err
	}
	// Ensure at least one permission so the role is usable.
	_ = db.Exec(
		`INSERT INTO role_permissions (role_id, permission)
		 VALUES (?, 'usage.read')
		 ON CONFLICT (role_id, permission) DO NOTHING`,
		id,
	)
	return id, nil
}
