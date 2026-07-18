// Package operator implements management-plane operator authentication (ADR-
// 0017): argon2id password hashing and the operator/role domain types. This is
// a DISTINCT system from client API-key auth (internal/auth, ADR-0006) — human
// operators log in with email + password; the two share no code path.
package operator

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Role is a management-plane operator role. Only two are enforced in phase-1
// (ADR-0017); the data model can carry more.
type Role string

const (
	// RoleSuperAdmin manages tenants and global platform config; no tenant bound.
	RoleSuperAdmin Role = "super-admin"
	// RoleTenantAdmin manages one tenant's groups/api_keys/quotas and reads its
	// usage; cannot touch global config.
	RoleTenantAdmin Role = "tenant-admin"
)

// Operator is a resolved management-plane operator. TenantID is 0/absent for a
// global-scope operator (tenant_id IS NULL) and set for a tenant-scoped operator.
type Operator struct {
	ID       int64
	Email    string
	Role     Role   // legacy text role (kept for migration; prefer RoleID)
	TenantID *int64 // nil for global scope

	// Phase-2 RBAC fields (populated from roles table via session lookup).
	RoleID      int64           // FK to roles.id
	ScopeKind   string          // "global" or "tenant" (from roles.scope_kind)
	Permissions map[string]bool // loaded permission set (or nil for wildcard)
}

// argon2id parameters (OWASP-recommended baseline). Encoded into each hash so
// verification is self-describing and params can evolve without breaking old
// hashes.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-format argon2id hash of the password (random salt
// per call). Store the returned string; verify with VerifyPassword.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the PHC-format argon2id hash.
// A malformed hash returns an error (not a silent false).
func VerifyPassword(password, encoded string) (bool, error) {
	salt, want, params, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	// Constant-time compare to avoid leaking match progress via timing.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func decodeHash(encoded string) (salt, key []byte, p argonParams, err error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=..", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, p, errors.New("operator: malformed argon2id hash")
	}
	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, p, err
	}
	if version != argon2.Version {
		return nil, nil, p, fmt.Errorf("operator: unsupported argon2 version %d", version)
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return nil, nil, p, err
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return nil, nil, p, err
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return nil, nil, p, err
	}
	return salt, key, p, nil
}
