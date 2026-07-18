//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/auth"
	"voxeltoad/internal/store"
)

// KeyRepo must satisfy auth.KeyStore — the data-plane Authenticator's fallback
// lookup (ADR-0006).
var _ auth.KeyStore = (*store.KeyRepo)(nil)

// seedKey inserts a tenant, group, and api_key, returning the hash. Each call
// uses distinct names so tests are independent within the shared DB.
func seedKey(t *testing.T, db *store.DB, keyID, hash, tenant, group string, expires *time.Time, revoked bool, allowed string) {
	t.Helper()
	seedKeyTenantEnabled(t, db, keyID, hash, tenant, group, expires, revoked, true, allowed)
}

// seedKeyTenantEnabled is seedKey with control over the owning tenant's
// enabled flag, so tests can exercise the disabled-tenant rejection path.
func seedKeyTenantEnabled(t *testing.T, db *store.DB, keyID, hash, tenant, group string, expires *time.Time, revoked, tenantEnabled bool, allowed string) {
	t.Helper()
	var tenantID, groupID int64
	if err := db.Raw(
		`INSERT INTO tenants (name, enabled) VALUES (?, ?) RETURNING id`, tenant, tenantEnabled,
	).Scan(&tenantID).Error; err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := db.Raw(
		`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`, tenantID, group,
	).Scan(&groupID).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	var revokedAt *time.Time
	if revoked {
		now := time.Now()
		revokedAt = &now
	}
	if err := db.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, expires_at, allowed_models, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?::jsonb, ?)`,
		keyID, hash, tenantID, groupID, expires, allowed, revokedAt,
	).Error; err != nil {
		t.Fatalf("insert api_key: %v", err)
	}
}

func TestKeyRepo_LookupByHash(t *testing.T) {
	ctx := context.Background()
	db := mustMigratedDB(t)
	repo := store.NewKeyRepo(db)

	hash := "a" + repeat("0", 63) // 64-char placeholder hash
	seedKey(t, db, "key_lookup", hash, "acme-lookup", "team-lookup", nil, false, `["gpt-4o","claude-3"]`)

	rec, ok, err := repo.LookupByHash(ctx, hash)
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for an existing key")
	}
	if rec.KeyID != "key_lookup" || rec.Tenant != "acme-lookup" || rec.Group != "team-lookup" {
		t.Errorf("record identity = %+v, want key_lookup/acme-lookup/team-lookup", rec)
	}
	if rec.Hash != hash {
		t.Errorf("Hash = %q, want %q", rec.Hash, hash)
	}
	if len(rec.AllowedModels) != 2 || rec.AllowedModels[0] != "gpt-4o" || rec.AllowedModels[1] != "claude-3" {
		t.Errorf("AllowedModels = %v, want [gpt-4o claude-3]", rec.AllowedModels)
	}
	if rec.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", rec.ExpiresAt)
	}
}

func TestKeyRepo_UnknownHash(t *testing.T) {
	repo := store.NewKeyRepo(mustMigratedDB(t))
	_, ok, err := repo.LookupByHash(context.Background(), "b"+repeat("0", 63))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if ok {
		t.Error("expected ok=false for an unknown hash")
	}
}

func TestKeyRepo_RevokedKeyNotFound(t *testing.T) {
	db := mustMigratedDB(t)
	repo := store.NewKeyRepo(db)
	hash := "c" + repeat("0", 63)
	seedKey(t, db, "key_revoked", hash, "acme-revoked", "team-revoked", nil, true, `[]`)

	_, ok, err := repo.LookupByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if ok {
		t.Error("revoked key (revoked_at set) must not be found")
	}
}

// A key owned by a disabled tenant must not be found: disabling a tenant
// rejects every API key under it, without touching the keys themselves
// (mirrors the revoked_at pattern above, but at the tenant level).
func TestKeyRepo_DisabledTenantNotFound(t *testing.T) {
	db := mustMigratedDB(t)
	repo := store.NewKeyRepo(db)
	hash := "e" + repeat("0", 63)
	seedKeyTenantEnabled(t, db, "key_disabled_tenant", hash, "acme-disabled", "team-disabled", nil, false, false, `[]`)

	_, ok, err := repo.LookupByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	if ok {
		t.Error("key under a disabled tenant must not be found")
	}
}

func TestKeyRepo_ExpiresAtPopulated(t *testing.T) {
	db := mustMigratedDB(t)
	repo := store.NewKeyRepo(db)
	hash := "d" + repeat("0", 63)
	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	seedKey(t, db, "key_exp", hash, "acme-exp", "team-exp", &exp, false, `[]`)

	rec, ok, err := repo.LookupByHash(context.Background(), hash)
	if err != nil || !ok {
		t.Fatalf("LookupByHash = ok %v err %v", ok, err)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be populated")
	}
	if !rec.ExpiresAt.UTC().Truncate(time.Second).Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", rec.ExpiresAt.UTC().Truncate(time.Second), exp)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}
