package store

import (
	"context"
	"fmt"

	"voxeltoad/internal/credential"
)

// CredentialRepo persists encrypted provider credentials in PostgreSQL. It only
// stores ciphertext; the plaintext key is handled by the credential.Service and
// never written to disk by this repo.
type CredentialRepo struct {
	db *DB
}

// NewCredentialRepo builds a credential repository over db.
func NewCredentialRepo(db *DB) *CredentialRepo {
	return &CredentialRepo{db: db}
}

// Upsert stores an encrypted credential for a provider, replacing any existing
// row. providerName must already exist in the providers table (enforced by the
// FK).
func (r *CredentialRepo) Upsert(ctx context.Context, providerName string, cred credential.Credential) error {
	err := r.db.WithContext(ctx).Exec(
		`INSERT INTO provider_credentials (provider_name, ciphertext, nonce, algorithm, key_version, updated_at)
		 VALUES (?, ?, ?, ?, ?, now())
		 ON CONFLICT (provider_name) DO UPDATE SET
		     ciphertext = EXCLUDED.ciphertext,
		     nonce = EXCLUDED.nonce,
		     algorithm = EXCLUDED.algorithm,
		     key_version = EXCLUDED.key_version,
		     updated_at = now()`,
		providerName, cred.Ciphertext, cred.Nonce, cred.Algorithm, cred.KeyVersion,
	).Error
	if err != nil {
		return fmt.Errorf("store: upsert credential for %q: %w", providerName, err)
	}
	return nil
}

// Get loads the encrypted credential for a provider. ok is false when no row
// exists.
func (r *CredentialRepo) Get(ctx context.Context, providerName string) (credential.Credential, bool, error) {
	var row struct {
		Ciphertext []byte
		Nonce      []byte
		Algorithm  string
		KeyVersion string
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT ciphertext, nonce, algorithm, key_version
		 FROM provider_credentials WHERE provider_name = ?`,
		providerName,
	).Scan(&row).Error; err != nil {
		return credential.Credential{}, false, fmt.Errorf("store: get credential for %q: %w", providerName, err)
	}
	if row.Ciphertext == nil { // no row found; GORM scans nil bytes for missing rows
		return credential.Credential{}, false, nil
	}
	return credential.Credential{
		ProviderName: providerName,
		Ciphertext:   row.Ciphertext,
		Nonce:        row.Nonce,
		Algorithm:    row.Algorithm,
		KeyVersion:   row.KeyVersion,
	}, true, nil
}

// Delete removes a credential row. Deleting the provider itself should cascade
// via the FK; this method is provided for explicit credential rotation cleanup.
func (r *CredentialRepo) Delete(ctx context.Context, providerName string) error {
	if err := r.db.WithContext(ctx).Exec(
		`DELETE FROM provider_credentials WHERE provider_name = ?`, providerName,
	).Error; err != nil {
		return fmt.Errorf("store: delete credential for %q: %w", providerName, err)
	}
	return nil
}
