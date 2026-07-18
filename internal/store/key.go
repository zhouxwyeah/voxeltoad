package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"voxeltoad/internal/auth"
)

// KeyRepo is the PostgreSQL implementation of auth.KeyStore (ADR-0006): the
// authoritative fallback the data-plane Authenticator consults on a cache miss.
// Keys are looked up by hash; revoked keys (revoked_at set) are treated as
// absent, as are keys owned by a disabled tenant (tenants.enabled = false) —
// disabling a tenant rejects all of its API keys without touching the keys
// themselves, mirroring the revoked_at soft-delete but reversible. Tenant/Group
// are returned as names (joined), matching auth.KeyRecord.
type KeyRepo struct {
	db *DB
}

// NewKeyRepo builds a KeyRepo over the given connection.
func NewKeyRepo(db *DB) *KeyRepo { return &KeyRepo{db: db} }

// LookupByHash returns the record for a key hash. ok is false if no active
// (non-revoked) key with that hash exists.
func (r *KeyRepo) LookupByHash(ctx context.Context, hash string) (auth.KeyRecord, bool, error) {
	var row struct {
		KeyID         string
		Tenant        string
		GroupName     sql.NullString
		ExpiresAt     sql.NullTime
		AllowedModels []byte
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT k.key_id        AS key_id,
		        t.name          AS tenant,
		        g.name          AS group_name,
		        k.expires_at    AS expires_at,
		        k.allowed_models AS allowed_models
		 FROM api_keys k
		 JOIN tenants t ON t.id = k.tenant_id
		 LEFT JOIN groups g ON g.id = k.group_id
		 WHERE k.hash = ? AND k.revoked_at IS NULL AND t.enabled = true`, hash,
	).Scan(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return auth.KeyRecord{}, false, nil
		}
		return auth.KeyRecord{}, false, err
	}
	// gorm's Raw+Scan into a struct yields a zero value (no error) when no row
	// matches; KeyID empty means "not found".
	if row.KeyID == "" {
		return auth.KeyRecord{}, false, nil
	}

	var allowed []string
	if len(row.AllowedModels) > 0 {
		if err := json.Unmarshal(row.AllowedModels, &allowed); err != nil {
			return auth.KeyRecord{}, false, err
		}
	}

	rec := auth.KeyRecord{
		KeyID:         row.KeyID,
		Tenant:        row.Tenant,
		Group:         row.GroupName.String,
		Hash:          hash,
		AllowedModels: allowed,
	}
	if row.ExpiresAt.Valid {
		exp := row.ExpiresAt.Time
		rec.ExpiresAt = &exp
	}
	return rec, true, nil
}
