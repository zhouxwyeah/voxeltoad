package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"voxeltoad/internal/operator"
)

// OperatorRepo persists management-plane operators (ADR-0017). Distinct from the
// client-key path (KeyRepo); operators authenticate with email + argon2id
// password hash.
type OperatorRepo struct {
	db *DB
}

// NewOperatorRepo builds an OperatorRepo over the given connection.
func NewOperatorRepo(db *DB) *OperatorRepo { return &OperatorRepo{db: db} }

// Create inserts an operator and returns it with its assigned ID. The role_id is
// resolved from the role name and included in the insert (Phase-2 RBAC).
// tenantID is nil for a global role, set for a tenant role.
func (r *OperatorRepo) Create(ctx context.Context, email, passwordHash string, role operator.Role, tenantID *int64) (operator.Operator, error) {
	var id int64
	err := r.db.WithContext(ctx).Raw(
		`INSERT INTO operators (email, password_hash, role, role_id, tenant_id)
		 VALUES (?, ?, ?, (SELECT id FROM roles WHERE name = ?), ?) RETURNING id`,
		email, passwordHash, string(role), string(role), tenantID,
	).Scan(&id).Error
	if err != nil {
		return operator.Operator{}, err
	}
	return operator.Operator{ID: id, Email: email, Role: role, TenantID: tenantID}, nil
}

// GetByEmail returns the operator and its stored password hash for verification.
// ok is false if no operator has that email.
func (r *OperatorRepo) GetByEmail(ctx context.Context, email string) (op operator.Operator, passwordHash string, ok bool, err error) {
	var row struct {
		ID           int64
		Email        string
		PasswordHash string
		Role         string
		TenantID     sql.NullInt64
	}
	err = r.db.WithContext(ctx).Raw(
		`SELECT id, email, password_hash, role, tenant_id FROM operators WHERE email = ?`, email,
	).Scan(&row).Error
	if err != nil {
		return operator.Operator{}, "", false, err
	}
	if row.ID == 0 {
		return operator.Operator{}, "", false, nil
	}
	return toOperator(row.ID, row.Email, row.Role, row.TenantID), row.PasswordHash, true, nil
}

// CountByRole returns how many operators hold the given role (used by bootstrap
// to check for an existing super-admin).
func (r *OperatorRepo) CountByRole(ctx context.Context, role operator.Role) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM operators WHERE role = ?`, string(role),
	).Scan(&n).Error
	return n, err
}

// OperatorInfo is a non-secret view of an operator (for listing) — never
// includes the password hash.
type OperatorInfo struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	TenantID *int64 `json:"tenant_id"`
}

// List returns non-secret operator views ordered by id, keyset-paginated: pass
// the previous page's nextCursor (empty for the first page). nextCursor is ""
// when there are no further pages. limit is capped by the caller.
func (r *OperatorRepo) List(ctx context.Context, cursor string, limit int) ([]OperatorInfo, string, error) {
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
		ID       int64
		Email    string
		Role     string
		TenantID sql.NullInt64
	}
	// Fetch limit+1 to detect a following page.
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, email, role, tenant_id FROM operators
		 WHERE id > ? ORDER BY id ASC LIMIT ?`, afterID, limit+1,
	).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	out := make([]OperatorInfo, 0, len(rows))
	for _, row := range rows {
		info := OperatorInfo{ID: row.ID, Email: row.Email, Role: row.Role}
		if row.TenantID.Valid {
			t := row.TenantID.Int64
			info.TenantID = &t
		}
		out = append(out, info)
	}

	next := ""
	if len(out) > limit {
		next = encodeIDCursor(out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

// Delete removes an operator by id and reports whether a row was actually
// deleted (false if the id is unknown). Sessions cascade via the FK
// (ON DELETE CASCADE); callers may also revoke sessions explicitly first.
func (r *OperatorRepo) Delete(ctx context.Context, id int64) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`DELETE FROM operators WHERE id = ?`, id)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// Update changes email, password_hash, role_id, and/or tenant_id for the operator
// identified by id. Empty strings for email/passwordHash mean "don't update";
// nil tenantID means "don't update"; nil roleID means "don't update".
// Returns false when id does not exist.
func (r *OperatorRepo) Update(ctx context.Context, id int64, email, passwordHash string, roleID *int64, tenantID *int64) (bool, error) {
	sets := []string{}
	args := []interface{}{}

	if email != "" {
		sets = append(sets, "email = ?")
		args = append(args, email)
	}
	if passwordHash != "" {
		sets = append(sets, "password_hash = ?")
		args = append(args, passwordHash)
	}
	if roleID != nil {
		sets = append(sets, "role_id = ?")
		args = append(args, *roleID)
	}
	if tenantID != nil {
		sets = append(sets, "tenant_id = ?")
		args = append(args, *tenantID)
	}

	if len(sets) == 0 {
		return false, nil
	}

	args = append(args, id)
	sql := "UPDATE operators SET " +
		strings.Join(sets, ", ") +
		" WHERE id = ?"

	res := r.db.WithContext(ctx).Exec(sql, args...)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func toOperator(id int64, email, role string, tenantID sql.NullInt64) operator.Operator {
	op := operator.Operator{ID: id, Email: email, Role: operator.Role(role)}
	if tenantID.Valid {
		t := tenantID.Int64
		op.TenantID = &t
	}
	return op
}

// encodeIDCursor / decodeIDCursor pack a single int64 id into an opaque keyset
// cursor (id-ordered lists). Distinct from the usage/audit (created_at,id)
// cursor codec, which orders by time.
func encodeIDCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeIDCursor(s string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, fmt.Errorf("id cursor: %w", err)
	}
	id, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("id cursor value: %w", err)
	}
	return id, nil
}

// SessionRepo persists opaque server-side operator sessions (ADR-0017),
// revocable instantly.
type SessionRepo struct {
	db *DB
}

// NewSessionRepo builds a SessionRepo over the given connection.
func NewSessionRepo(db *DB) *SessionRepo { return &SessionRepo{db: db} }

// Create stores a session token for an operator with an absolute expiry.
func (r *SessionRepo) Create(ctx context.Context, token string, operatorID int64, expiresAt time.Time) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO sessions (token, operator_id, expires_at) VALUES (?, ?, ?)`,
		token, operatorID, expiresAt,
	).Error
}

// Lookup resolves a non-expired session to its operator, loading the role's
// permission set and scope_kind from the roles table (Phase-2 RBAC).
// ok is false for an unknown or expired token.
func (r *SessionRepo) Lookup(ctx context.Context, token string) (op operator.Operator, ok bool, err error) {
	var row struct {
		ID       int64
		Email    string
		Role     string
		RoleID   sql.NullInt64
		TenantID sql.NullInt64
	}
	err = r.db.WithContext(ctx).Raw(
		`SELECT o.id, o.email, o.role, o.role_id, o.tenant_id
		 FROM sessions s JOIN operators o ON o.id = s.operator_id
		 WHERE s.token = ? AND s.expires_at > now()`, token,
	).Scan(&row).Error
	if err != nil {
		return operator.Operator{}, false, err
	}
	if row.ID == 0 {
		return operator.Operator{}, false, nil
	}
	op = toOperator(row.ID, row.Email, row.Role, row.TenantID)
	if row.RoleID.Valid {
		op.RoleID = row.RoleID.Int64
		// Load scope_kind from roles.
		var scopeKind string
		_ = r.db.WithContext(ctx).Raw(
			`SELECT scope_kind FROM roles WHERE id = ?`, op.RoleID,
		).Scan(&scopeKind)
		op.ScopeKind = scopeKind

		// Load permission set from role_permissions.
		var perms []string
		_ = r.db.WithContext(ctx).Raw(
			`SELECT permission FROM role_permissions WHERE role_id = ?`, op.RoleID,
		).Scan(&perms)
		op.Permissions = make(map[string]bool, len(perms))
		for _, p := range perms {
			op.Permissions[p] = true
		}
	}
	return op, true, nil
}

// Delete revokes a single session (logout).
func (r *SessionRepo) Delete(ctx context.Context, token string) error {
	return r.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE token = ?`, token).Error
}

// DeleteByOperator revokes all of an operator's sessions (firing / global
// logout).
func (r *SessionRepo) DeleteByOperator(ctx context.Context, operatorID int64) error {
	return r.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE operator_id = ?`, operatorID).Error
}
