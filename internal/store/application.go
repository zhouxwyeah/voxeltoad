package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ApplicationRepo is a tenant-scoped repository for Application governance
// entities (ADR-0051). The tenant id is bound at construction and injected into
// every query; there is NO method to specify a different tenant, so a handler
// holding an ApplicationRepo physically cannot read or write another tenant's
// rows (ADR-0017 §3 — structural isolation, same pattern as TenantRepo).
type ApplicationRepo struct {
	db       *DB
	tenantID int64
}

// NewApplicationRepo builds a repository scoped to a single tenant.
func NewApplicationRepo(db *DB, tenantID int64) *ApplicationRepo {
	return &ApplicationRepo{db: db, tenantID: tenantID}
}

// Application is a tenant-scoped governance identity (ADR-0051). OwnerGroupName
// is JOIN-resolved for the admin UI.
type Application struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	OwnerGroupID   int64  `json:"owner_group_id"`
	OwnerGroupName string `json:"owner_group_name"`
	Enabled        bool   `json:"enabled"`
}

// Create inserts an Application owned by ownerGroupName within the bound tenant.
// Returns the new id. Fails on UNIQUE(tenant_id, name) violation or unknown
// owner group (FK).
func (r *ApplicationRepo) Create(ctx context.Context, name, ownerGroupName string) (int64, error) {
	var id int64
	err := r.db.WithContext(ctx).Raw(
		`INSERT INTO applications (tenant_id, name, owner_group_id)
		 VALUES (?, ?,
		         (SELECT id FROM groups WHERE tenant_id = ? AND name = ?))
		 RETURNING id`,
		r.tenantID, name, r.tenantID, ownerGroupName,
	).Scan(&id).Error
	return id, err
}

// List returns a keyset-paginated page of the bound tenant's applications,
// ordered by id. cursor is an opaque id cursor ("" for the first page).
// limit <= 0 defaults to 50. next is "" when there is no further page.
func (r *ApplicationRepo) List(ctx context.Context, cursor string, limit int) ([]Application, string, error) {
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
		ID             int64
		Name           string
		OwnerGroupID   int64
		OwnerGroupName string
		Enabled        bool
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT a.id, a.name, a.owner_group_id, g.name AS owner_group_name, a.enabled
		 FROM applications a
		 JOIN groups g ON g.id = a.owner_group_id
		 WHERE a.tenant_id = ? AND a.id > ?
		 ORDER BY a.id ASC LIMIT ?`,
		r.tenantID, afterID, limit+1,
	).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	out := make([]Application, 0, len(rows))
	for _, row := range rows {
		out = append(out, Application{
			ID:             row.ID,
			Name:           row.Name,
			OwnerGroupID:   row.OwnerGroupID,
			OwnerGroupName: row.OwnerGroupName,
			Enabled:        row.Enabled,
		})
	}

	next := ""
	if len(out) > limit {
		next = encodeIDCursor(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

// Get returns one application by name within the bound tenant. ok is false
// when no application with that name exists in this tenant.
func (r *ApplicationRepo) Get(ctx context.Context, name string) (Application, bool, error) {
	var row struct {
		ID             int64
		Name           string
		OwnerGroupID   int64
		OwnerGroupName string
		Enabled        bool
	}
	err := r.db.WithContext(ctx).Raw(
		`SELECT a.id, a.name, a.owner_group_id, g.name AS owner_group_name, a.enabled
		 FROM applications a
		 JOIN groups g ON g.id = a.owner_group_id
		 WHERE a.tenant_id = ? AND a.name = ?`,
		r.tenantID, name,
	).Scan(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Application{}, false, nil
		}
		return Application{}, false, err
	}
	if row.ID == 0 {
		return Application{}, false, nil
	}
	return Application{
		ID:             row.ID,
		Name:           row.Name,
		OwnerGroupID:   row.OwnerGroupID,
		OwnerGroupName: row.OwnerGroupName,
		Enabled:        row.Enabled,
	}, true, nil
}

// SetEnabled flips the enabled flag (reversible, mirrors SetGroupEnabled).
// Disabling an application uniformly stops all bound credentials (ADR-0051);
// the enforcement is in KeyRepo.LookupByHash, not here — no cascading write.
// ok is false when no application with that name exists in the bound tenant.
func (r *ApplicationRepo) SetEnabled(ctx context.Context, name string, enabled bool) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		`UPDATE applications SET enabled = ?, updated_at = now()
		 WHERE tenant_id = ? AND name = ?`,
		enabled, r.tenantID, name,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// Delete removes an application by name within the bound tenant. ok is false
// when not found. PG's FK default (RESTRICT) blocks deletion when api_keys
// reference the application; callers should check ApplicationReferencedByAPIKeys
// first and return a clean 409.
func (r *ApplicationRepo) Delete(ctx context.Context, name string) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		`DELETE FROM applications WHERE tenant_id = ? AND name = ?`,
		r.tenantID, name,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ApplicationReferencedByAPIKeys returns the key_id of every active
// (non-revoked) api_key bound to the named application. Empty when no
// references exist. Mirrors GroupReferencedByAPIKeys.
func (r *ApplicationRepo) ApplicationReferencedByAPIKeys(ctx context.Context, name string) ([]string, error) {
	var rows []struct {
		KeyID string
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT k.key_id
		 FROM api_keys k
		 JOIN applications a ON a.id = k.application_id
		 WHERE k.tenant_id = ? AND a.tenant_id = ? AND a.name = ? AND k.revoked_at IS NULL`,
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

// OrphanKeys returns the key_id of every active (non-revoked) api_key in the
// bound tenant that has no Application binding (application_id IS NULL). This
// quantifies migration debt so operators can track unattributed traffic
// (ADR-0051: unbound keys are measurable migration debt, not a supported
// long-term mode).
func (r *ApplicationRepo) OrphanKeys(ctx context.Context) ([]string, error) {
	var rows []struct {
		KeyID string
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT key_id FROM api_keys
		 WHERE tenant_id = ? AND application_id IS NULL AND revoked_at IS NULL`,
		r.tenantID,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.KeyID
	}
	return out, nil
}
