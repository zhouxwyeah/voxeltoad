package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
)

// TenantRepo is a tenant-scoped repository for tenant-owned resources (groups,
// api_keys). The tenant id is bound at construction and injected into every
// query; there is NO method to specify a different tenant, so a handler holding
// a TenantRepo physically cannot read or write another tenant's rows (ADR-0017
// §3 — structural isolation, testable in pure Go). super-admin operations on
// global resources use the unscoped repos instead.
type TenantRepo struct {
	db       *DB
	tenantID int64
}

// NewTenantRepo builds a repository scoped to a single tenant. The authz layer
// constructs it from the operator's tenant (ADR-0017); nothing downstream can
// widen the scope.
func NewTenantRepo(db *DB, tenantID int64) *TenantRepo {
	return &TenantRepo{db: db, tenantID: tenantID}
}

// TenantInfo is a tenant row (global view, super-admin).
type TenantInfo struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// CreateTenant inserts a tenant (super-admin, global) and returns its id. New
// tenants default to enabled=true (schema default).
func CreateTenant(ctx context.Context, db *DB, name string) (int64, error) {
	var id int64
	err := db.WithContext(ctx).Raw(
		`INSERT INTO tenants (name) VALUES (?) RETURNING id`, name,
	).Scan(&id).Error
	return id, err
}

// ListTenants returns a keyset-paginated page of tenants (super-admin,
// global), ordered by id. cursor is an opaque id cursor from a prior call's
// next return value ("" for the first page); limit <= 0 defaults to 50. next
// is "" when there is no further page. Mirrors OperatorRepo.List's pagination
// shape (see operator.go), reusing the same encodeIDCursor/decodeIDCursor.
func ListTenants(ctx context.Context, db *DB, cursor string, limit int) ([]TenantInfo, string, error) {
	if limit <= 0 {
		limit = 50
	}
	var afterID int64
	if cursor != "" {
		id, err := decodeIDCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		afterID = id
	}

	var rows []struct {
		ID      int64
		Name    string
		Enabled bool
	}
	// Fetch limit+1 to detect a following page.
	if err := db.WithContext(ctx).Raw(
		`SELECT id, name, enabled FROM tenants WHERE id > ? ORDER BY id ASC LIMIT ?`,
		afterID, limit+1,
	).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	out := make([]TenantInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, TenantInfo{ID: row.ID, Name: row.Name, Enabled: row.Enabled})
	}

	next := ""
	if len(out) > limit {
		next = encodeIDCursor(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

// SetTenantEnabled flips a tenant's enabled flag (reversible: unlike
// api_keys.revoked_at, this is not a one-way soft-delete — a disabled tenant
// can be re-enabled). ok is false when no tenant with that name exists.
// Disabling a tenant is enforced at the data-plane authentication boundary
// (KeyRepo.LookupByHash, see key.go), not by touching any other table: every
// API key under a disabled tenant is rejected without a cascading write.
func SetTenantEnabled(ctx context.Context, db *DB, name string, enabled bool) (bool, error) {
	res := db.WithContext(ctx).Exec(
		`UPDATE tenants SET enabled = ?, updated_at = now() WHERE name = ?`,
		enabled, name,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// TenantName resolves a tenant's name from its id. Used to scope name-keyed
// reads (usage/audit rows store the tenant name, not id) to an operator's
// tenant. Returns ("", nil) when the id is unknown.
func TenantName(ctx context.Context, db *DB, id int64) (string, error) {
	var name string
	err := db.WithContext(ctx).Raw(`SELECT name FROM tenants WHERE id = ?`, id).Scan(&name).Error
	return name, err
}

// TenantExists reports whether a tenant with the given name exists. Used when
// a super-admin explicitly scopes a usage/audit query to a single tenant by name.
func TenantExists(ctx context.Context, db *DB, name string) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Raw(`SELECT count(*) FROM tenants WHERE name = ?`, name).Scan(&count).Error
	return count > 0, err
}

// Group is a tenant group row (scoped view).
type Group struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// CreateGroup inserts a group owned by the bound tenant and returns its id.
func (r *TenantRepo) CreateGroup(ctx context.Context, name string) (int64, error) {
	var id int64
	err := r.db.WithContext(ctx).Raw(
		`INSERT INTO groups (tenant_id, name) VALUES (?, ?) RETURNING id`,
		r.tenantID, name,
	).Scan(&id).Error
	return id, err
}

// ListGroups returns a keyset-paginated page of the bound tenant's groups,
// ordered by id. cursor is an opaque id cursor ("" for the first page).
// limit <= 0 defaults to 50. next is "" when there is no further page.
func (r *TenantRepo) ListGroups(ctx context.Context, cursor string, limit int) ([]Group, string, error) {
	if limit <= 0 {
		limit = 50
	}
	var afterID int64
	if cursor != "" {
		id, err := decodeIDCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		afterID = id
	}

	var rows []struct {
		ID      int64
		Name    string
		Enabled bool
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, name, enabled FROM groups WHERE tenant_id = ? AND id > ? ORDER BY id ASC LIMIT ?`,
		r.tenantID, afterID, limit+1,
	).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	out := make([]Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, Group{ID: row.ID, Name: row.Name, Enabled: row.Enabled})
	}

	next := ""
	if len(out) > limit {
		next = encodeIDCursor(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

// SetGroupEnabled flips a group's enabled flag (reversible, mirrors
// SetTenantEnabled). ok is false when no group with that name exists in the
// bound tenant.
func (r *TenantRepo) SetGroupEnabled(ctx context.Context, name string, enabled bool) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		`UPDATE groups SET enabled = ?, updated_at = now() WHERE tenant_id = ? AND name = ?`,
		enabled, r.tenantID, name,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// DeleteGroup removes a group by name within the bound tenant. ok is false
// when no group with that name exists in the bound tenant. PG's FK default
// (RESTRICT) blocks deletion when api_keys reference the group; callers
// should check GroupReferencedByAPIKeys first and return a clean 409.
func (r *TenantRepo) DeleteGroup(ctx context.Context, name string) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		`DELETE FROM groups WHERE tenant_id = ? AND name = ?`,
		r.tenantID, name,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// GroupReferencedByAPIKeys includes revoked keys: their retained rows still
// reference the group and prevent deletion.
func (r *TenantRepo) GroupReferencedByAPIKeys(ctx context.Context, name string) ([]string, error) {
	var rows []struct {
		KeyID string
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT k.key_id
		 FROM api_keys k
		 JOIN groups g ON g.id = k.group_id
		 WHERE k.tenant_id = ? AND g.tenant_id = ? AND g.name = ?`,
		r.tenantID, r.tenantID, name,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.KeyID
	}
	return out, nil
}

// GroupReferencedByApplications returns applications maintained by the group.
func (r *TenantRepo) GroupReferencedByApplications(ctx context.Context, name string) ([]string, error) {
	var names []string
	err := r.db.WithContext(ctx).Raw(
		`SELECT a.name FROM applications a JOIN groups g ON g.id = a.owner_group_id
		 WHERE a.tenant_id = ? AND g.tenant_id = ? AND g.name = ? ORDER BY a.id`,
		r.tenantID, r.tenantID, name,
	).Scan(&names).Error
	return names, err
}

var (
	ErrInvalidAPIKeyIdentity   = errors.New("group_id, application_id and environment must identify enabled resources in this tenant")
	ErrAPIKeyIdentityImmutable = errors.New("existing API key governance identity cannot be changed; issue a new key instead")
)

// APIKeyIdentity is the complete, immutable enterprise credential identity.
// GroupID is the consuming group, not the application's owner group.
type APIKeyIdentity struct {
	GroupID       int64  `json:"group_id"`
	ApplicationID int64  `json:"application_id"`
	Environment   string `json:"environment"`
}

func (i APIKeyIdentity) valid() bool {
	return i.GroupID > 0 && i.ApplicationID > 0 &&
		(i.Environment == "dev" || i.Environment == "staging" || i.Environment == "prod")
}

// APIKeySpec is the input for creating a client API key (the hash is computed by
// the caller; plaintext is never stored — ADR-0006).
type APIKeySpec struct {
	KeyID         string
	Hash          string
	GroupID       *int64
	AllowedModels []string
	// ApplicationID binds the key to an Application (ADR-0051). nil = unbound
	// (migration debt). At most one Application per key.
	ApplicationID *int64
	// Environment is a controlled credential attribute (dev/staging/prod/'').
	Environment string
}

// APIKeyInfo is a non-secret view of an API key (for listing).
type APIKeyInfo struct {
	KeyID         string   `json:"key_id"`
	Revoked       bool     `json:"revoked"`
	AllowedModels []string `json:"allowed_models,omitempty"`
	GroupID       *int64   `json:"group_id"`
	// ApplicationID is nil for unbound keys (migration debt).
	ApplicationID *int64 `json:"application_id"`
	Environment   string `json:"environment"`
}

// CreateAPIKey supports internal legacy seeds with incomplete identity. Public
// enterprise issuance must use CreateEnterpriseAPIKey instead.
func (r *TenantRepo) CreateAPIKey(ctx context.Context, spec APIKeySpec) error {
	return r.createAPIKey(r.db.WithContext(ctx), spec)
}

// CreateEnterpriseAPIKey validates and inserts the complete identity atomically.
func (r *TenantRepo) CreateEnterpriseAPIKey(ctx context.Context, spec APIKeySpec) error {
	if spec.GroupID == nil || spec.ApplicationID == nil {
		return ErrInvalidAPIKeyIdentity
	}
	identity := APIKeyIdentity{GroupID: *spec.GroupID, ApplicationID: *spec.ApplicationID, Environment: spec.Environment}
	if !identity.valid() {
		return ErrInvalidAPIKeyIdentity
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.validateAPIKeyIdentity(tx, identity); err != nil {
			return err
		}
		return r.createAPIKey(tx, spec)
	})
}

func (r *TenantRepo) validateAPIKeyIdentity(tx *gorm.DB, identity APIKeyIdentity) error {
	// SHARE locks serialize enable/disable and deletion with identity binding.
	var exists bool
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM tenants WHERE id = ? AND enabled FOR SHARE)`, r.tenantID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return ErrInvalidAPIKeyIdentity
	}
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM groups WHERE id = ? AND tenant_id = ? AND enabled FOR SHARE)`, identity.GroupID, r.tenantID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return ErrInvalidAPIKeyIdentity
	}
	if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM applications WHERE id = ? AND tenant_id = ? AND enabled FOR SHARE)`, identity.ApplicationID, r.tenantID).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return ErrInvalidAPIKeyIdentity
	}
	return nil
}

func (r *TenantRepo) createAPIKey(tx *gorm.DB, spec APIKeySpec) error {
	allowed := spec.AllowedModels
	if allowed == nil {
		allowed = []string{}
	}
	models, err := json.Marshal(allowed)
	if err != nil {
		return err
	}
	return tx.Exec(
		`INSERT INTO api_keys (key_id, hash, tenant_id, group_id, allowed_models, application_id, environment)
		 VALUES (?, ?, ?, ?, ?::jsonb, ?, ?)`,
		spec.KeyID, spec.Hash, r.tenantID, spec.GroupID, string(models), spec.ApplicationID, spec.Environment,
	).Error
}

// ListAPIKeys returns a keyset-paginated page of non-secret info for the bound
// tenant's active (non-revoked) keys, ordered by id. cursor is an opaque id
// cursor ("" for the first page). limit <= 0 defaults to 50. next is "" when
// there is no further page.
func (r *TenantRepo) ListAPIKeys(ctx context.Context, cursor string, limit int) ([]APIKeyInfo, string, error) {
	return r.ListAPIKeysFiltered(ctx, cursor, limit, false)
}

// ListAPIKeysFiltered restricts migration debt to active keys missing any part
// of their identity, without changing pagination or the tenant boundary.
func (r *TenantRepo) ListAPIKeysFiltered(ctx context.Context, cursor string, limit int, unbound bool) ([]APIKeyInfo, string, error) {
	if limit <= 0 {
		limit = 50
	}
	var afterID int64
	if cursor != "" {
		id, err := decodeIDCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		afterID = id
	}

	var rows []struct {
		InternalID    int64
		KeyID         string
		AllowedModels string
		GroupID       sql.NullInt64
		ApplicationID sql.NullInt64
		Environment   string
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id AS internal_id, key_id,
		        COALESCE(allowed_models::text, '[]') AS allowed_models,
		        group_id, application_id, environment
		 FROM api_keys
		 WHERE tenant_id = ? AND id > ? AND revoked_at IS NULL
		   AND (NOT ? OR group_id IS NULL OR application_id IS NULL OR environment = '')
		 ORDER BY id ASC LIMIT ?`,
		r.tenantID, afterID, unbound, limit+1,
	).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	out := make([]APIKeyInfo, 0, len(rows))
	for _, row := range rows {
		var models []string
		_ = json.Unmarshal([]byte(row.AllowedModels), &models)
		info := APIKeyInfo{
			KeyID:         row.KeyID,
			Revoked:       false,
			AllowedModels: models,
			Environment:   row.Environment,
		}
		if row.GroupID.Valid {
			groupID := row.GroupID.Int64
			info.GroupID = &groupID
		}
		if row.ApplicationID.Valid {
			appID := row.ApplicationID.Int64
			info.ApplicationID = &appID
		}
		out = append(out, info)
	}

	next := ""
	if len(out) > limit {
		next = encodeIDCursor(rows[limit-1].InternalID)
		out = out[:limit]
	}
	return out, next, nil
}

// RevokeAPIKey soft-deletes a key by key_id within the bound tenant. It reports
// whether a key was actually revoked (false if the key_id is unknown or belongs
// to another tenant — the tenant filter makes cross-tenant revocation a no-op).
func (r *TenantRepo) RevokeAPIKey(ctx context.Context, keyID string) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		`UPDATE api_keys SET revoked_at = now()
		 WHERE key_id = ? AND tenant_id = ? AND revoked_at IS NULL`,
		keyID, r.tenantID,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SetAPIKeyAllowedModels updates the allowed_models JSONB column for an active
// (non-revoked) key within the bound tenant. Returns false when the key_id is
// unknown, belongs to another tenant, or has already been revoked.
func (r *TenantRepo) SetAPIKeyAllowedModels(ctx context.Context, keyID string, models []string) (bool, error) {
	if models == nil {
		models = []string{}
	}
	js, err := json.Marshal(models)
	if err != nil {
		return false, err
	}
	res := r.db.WithContext(ctx).Exec(
		`UPDATE api_keys SET allowed_models = ?::jsonb
		 WHERE key_id = ? AND tenant_id = ? AND revoked_at IS NULL`,
		string(js), keyID, r.tenantID,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// APIKeyUpdate optionally changes model access and completes a legacy identity.
// Identity is all-or-nothing; non-empty existing identity fields are immutable.
type APIKeyUpdate struct {
	AllowedModels *[]string
	Identity      *APIKeyIdentity
}

// UpdateAPIKey locks the key before validating its current identity, so two
// concurrent completions cannot reassign a credential. No historical ledger is
// updated. An identical identity is an idempotent retry, even after disablement.
func (r *TenantRepo) UpdateAPIKey(ctx context.Context, keyID string, patch APIKeyUpdate) (bool, error) {
	if patch.Identity != nil && !patch.Identity.valid() {
		return false, ErrInvalidAPIKeyIdentity
	}
	var ok bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current struct {
			ID            int64
			GroupID       sql.NullInt64
			ApplicationID sql.NullInt64
			Environment   string
		}
		if err := tx.Raw(`SELECT id, group_id, application_id, environment FROM api_keys
			WHERE tenant_id = ? AND key_id = ? AND revoked_at IS NULL FOR UPDATE`,
			r.tenantID, keyID).Scan(&current).Error; err != nil {
			return err
		}
		if current.ID == 0 {
			return nil
		}
		if identity := patch.Identity; identity != nil {
			if (current.GroupID.Valid && current.GroupID.Int64 != identity.GroupID) ||
				(current.ApplicationID.Valid && current.ApplicationID.Int64 != identity.ApplicationID) ||
				(current.Environment != "" && current.Environment != identity.Environment) {
				return ErrAPIKeyIdentityImmutable
			}
			if !current.GroupID.Valid || !current.ApplicationID.Valid || current.Environment == "" {
				if err := r.validateAPIKeyIdentity(tx, *identity); err != nil {
					return err
				}
				if err := tx.Exec(`UPDATE api_keys SET group_id = ?, application_id = ?, environment = ?
					WHERE id = ? AND tenant_id = ?`, identity.GroupID, identity.ApplicationID, identity.Environment,
					current.ID, r.tenantID).Error; err != nil {
					return err
				}
			}
		}
		if patch.AllowedModels != nil {
			models := *patch.AllowedModels
			if models == nil {
				models = []string{}
			}
			js, err := json.Marshal(models)
			if err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE api_keys SET allowed_models = ?::jsonb WHERE id = ? AND tenant_id = ?`,
				string(js), current.ID, r.tenantID).Error; err != nil {
				return err
			}
		}
		ok = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return ok, nil
}
