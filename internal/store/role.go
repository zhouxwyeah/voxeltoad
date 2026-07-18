package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Role is one row of the roles table.
type Role struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ScopeKind   string `json:"scope_kind"` // "global" or "tenant"
	IsBuiltin   bool   `json:"is_builtin"`
	Description string `json:"description"`
}

// RoleRepo is the persistence layer for roles and their permission sets.
type RoleRepo struct {
	db *DB
}

func NewRoleRepo(db *DB) *RoleRepo { return &RoleRepo{db: db} }

// GetByID returns a single role. Returns nil, false when not found.
func (r *RoleRepo) GetByID(ctx context.Context, id int64) (*Role, bool, error) {
	var role Role
	err := r.db.WithContext(ctx).Raw(
		`SELECT id, name, scope_kind, is_builtin, description
		 FROM roles WHERE id = ?`, id,
	).Scan(&role).Error
	if err != nil {
		return nil, false, err
	}
	if role.ID == 0 {
		return nil, false, nil
	}
	return &role, true, nil
}

// GetByName returns a role by its unique name.
func (r *RoleRepo) GetByName(ctx context.Context, name string) (*Role, bool, error) {
	var role Role
	err := r.db.WithContext(ctx).Raw(
		`SELECT id, name, scope_kind, is_builtin, description
		 FROM roles WHERE name = ?`, name,
	).Scan(&role).Error
	if err != nil {
		return nil, false, err
	}
	if role.ID == 0 {
		return nil, false, nil
	}
	return &role, true, nil
}

// List returns all roles ordered by id (built-in first).
func (r *RoleRepo) List(ctx context.Context) ([]Role, error) {
	var roles []Role
	err := r.db.WithContext(ctx).Raw(
		`SELECT id, name, scope_kind, is_builtin, description
		 FROM roles ORDER BY is_builtin DESC, id`).Scan(&roles).Error
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []Role{}
	}
	return roles, nil
}

// LoadPermissions returns the permission set for a role. A row with permission="*"
// means the caller should treat this as "all permissions".
func (r *RoleRepo) LoadPermissions(ctx context.Context, roleID int64) ([]string, error) {
	var perms []string
	err := r.db.WithContext(ctx).Raw(
		`SELECT permission FROM role_permissions WHERE role_id = ?`, roleID,
	).Scan(&perms).Error
	if err != nil {
		return nil, err
	}
	return perms, nil
}

// Create inserts a new role with its permission set.
func (r *RoleRepo) Create(ctx context.Context, role *Role, permissions []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`INSERT INTO roles (name, scope_kind, is_builtin, description)
			 VALUES (?, ?, false, ?)`, role.Name, role.ScopeKind, role.Description,
		).Error; err != nil {
			return err
		}

		// Retrieve the new role's id for permission inserts.
		var id int64
		if err := tx.Raw(`SELECT id FROM roles WHERE name = ?`, role.Name).Scan(&id).Error; err != nil {
			return err
		}
		role.ID = id

		for _, p := range permissions {
			if err := tx.Exec(
				`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`,
				id, p,
			).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdatePermissions replaces the permission set for a role atomically.
func (r *RoleRepo) UpdatePermissions(ctx context.Context, roleID int64, permissions []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM role_permissions WHERE role_id = ?`, roleID).Error; err != nil {
			return err
		}
		for _, p := range permissions {
			if err := tx.Exec(
				`INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)`,
				roleID, p,
			).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateDescription sets the description of a non-builtin role.
func (r *RoleRepo) UpdateDescription(ctx context.Context, roleID int64, desc string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE roles SET description = ?, updated_at = now()
		 WHERE id = ? AND is_builtin = false`, desc, roleID,
	).Error
}

// Delete removes a non-builtin role that has no operators referencing it.
func (r *RoleRepo) Delete(ctx context.Context, roleID int64) error {
	var refCount int64
	if err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM operators WHERE role_id = ?`, roleID,
	).Scan(&refCount).Error; err != nil {
		return err
	}
	if refCount > 0 {
		return fmt.Errorf("role %d is in use by %d operators", roleID, refCount)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM role_permissions WHERE role_id = ?`, roleID).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM roles WHERE id = ? AND is_builtin = false`, roleID).Error
	})
}

// HasOperatorWithRole checks if any operator references the given role.
func (r *RoleRepo) HasOperatorWithRole(ctx context.Context, roleID int64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM operators WHERE role_id = ?`, roleID,
	).Scan(&count).Error
	return count > 0, err
}
