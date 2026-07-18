package desktopstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"voxeltoad/internal/auth"
)

// KeyStore is the desktop SQLite implementation of auth.KeyStore. The personal
// gateway has exactly one seeded default key; lookup resolves by SHA-256 hash
// with expiry/revocation checks. Empty AllowedModels means "all models" — which
// is how the default key grants unrestricted access without any RBAC (see
// design/desktop.md §8, K1).
type KeyStore struct {
	db *DB
}

// NewKeyStore builds a SQLite-backed auth.KeyStore.
func NewKeyStore(db *DB) *KeyStore { return &KeyStore{db: db} }

// LookupByHash implements auth.KeyStore.
func (k *KeyStore) LookupByHash(ctx context.Context, hash string) (auth.KeyRecord, bool, error) {
	var row APIKeyRow
	if err := k.db.WithContext(ctx).
		Where("hash = ? AND revoked_at IS NULL", hash).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return auth.KeyRecord{}, false, nil
		}
		return auth.KeyRecord{}, false, err
	}
	if row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now()) {
		return auth.KeyRecord{}, false, nil
	}
	var allowed []string
	if row.AllowedModels != "" {
		_ = json.Unmarshal([]byte(row.AllowedModels), &allowed)
	}
	return auth.KeyRecord{
		KeyID:         row.KeyID,
		Tenant:        row.Tenant,
		Group:         row.Group,
		Hash:          row.Hash,
		ExpiresAt:     row.ExpiresAt,
		AllowedModels: allowed,
	}, true, nil
}
