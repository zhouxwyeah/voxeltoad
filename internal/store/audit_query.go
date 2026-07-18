package store

import (
	"context"
	"strings"
	"time"
)

// AuditQueryRepo is the read side of audit_logs for the management plane
// (ADR-0019). READ-ONLY. Tenant is bound at construction: "" = global
// (super-admin sees all rows), a non-empty tenant scopes to rows attributed to
// that tenant — including super-admin actions on it (the tenant column is the
// AFFECTED tenant, ADR-0019). No method widens the scope.
type AuditQueryRepo struct {
	db     *DB
	tenant string // "" = global
}

// NewAuditQueryRepo builds a read repo. tenant=="" means global.
func NewAuditQueryRepo(db *DB, tenant string) *AuditQueryRepo {
	return &AuditQueryRepo{db: db, tenant: tenant}
}

// AuditFilter narrows an audit query. Empty fields are ignored.
type AuditFilter struct {
	ResourceType string
	ResourceID   string
	Action       string
	OperatorID   *int64 // nil = all operators
	From, To     time.Time
}

// AuditRow is one audit entry as returned to the management plane.
type AuditRow struct {
	ID           int64     `json:"id"`
	OperatorID   *int64    `json:"operator_id"`
	Tenant       *string   `json:"tenant"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	After        []byte    `json:"after"`
	CreatedAt    time.Time `json:"created_at"`
}

// buildWhere assembles the filter WHERE clauses (without the keyset cursor
// predicate) so List and ListPage/Count share the same scoping logic. The
// bound tenant is always applied first, so a tenant-admin COUNT never leaks
// cross-tenant totals.
func (r *AuditQueryRepo) buildWhere(f AuditFilter) (where []string, args []any) {
	where = []string{"1=1"}
	if r.tenant != "" {
		where = append(where, "tenant = ?")
		args = append(args, r.tenant)
	}
	if f.ResourceType != "" {
		where = append(where, "resource_type = ?")
		args = append(args, f.ResourceType)
	}
	if f.ResourceID != "" {
		where = append(where, "resource_id = ?")
		args = append(args, f.ResourceID)
	}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.OperatorID != nil {
		where = append(where, "operator_id = ?")
		args = append(args, *f.OperatorID)
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.To)
	}
	return where, args
}

// List returns a page of audit rows in (created_at, id) DESC order, bounded by
// the bound tenant and the given filter. cursor is an opaque keyset cursor
// (empty for the first page); nextCursor is "" when there are no further pages.
func (r *AuditQueryRepo) List(ctx context.Context, f AuditFilter, cursor string, limit int) ([]AuditRow, string, error) {
	if limit <= 0 {
		limit = 50
	}

	where, args := r.buildWhere(f)
	if cursor != "" {
		ct, cid, err := decodeUsageCursor(cursor) // same (created_at,id) encoding
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(created_at, id) < (?, ?)")
		args = append(args, ct, cid)
	}

	args = append(args, limit+1)
	q := `SELECT id, operator_id, tenant, action, resource_type, resource_id, after, created_at
	      FROM audit_logs
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ?`

	var rows []AuditRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, "", err
	}

	next := ""
	if len(rows) > limit {
		last := rows[limit-1]
		next = encodeUsageCursor(last.CreatedAt, last.ID)
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []AuditRow{} // marshal as [] not null (list envelope contract)
	}
	return rows, next, nil
}

// ListPage returns a single offset-paginated page of audit rows plus the total
// matching row count (for page-jump UIs). page is 1-based; pageSize must be > 0.
// COUNT(*) reuses buildWhere so the bound-tenant scope applies identically.
func (r *AuditQueryRepo) ListPage(ctx context.Context, f AuditFilter, page, pageSize int) ([]AuditRow, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}

	where, args := r.buildWhere(f)
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM audit_logs WHERE `+whereSQL, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	pageArgs := append(args, pageSize, offset)
	q := `SELECT id, operator_id, tenant, action, resource_type, resource_id, after, created_at
	      FROM audit_logs
	      WHERE ` + whereSQL + `
	      ORDER BY created_at DESC, id DESC
	      LIMIT ? OFFSET ?`

	var rows []AuditRow
	if err := r.db.WithContext(ctx).Raw(q, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	if rows == nil {
		rows = []AuditRow{} // marshal as [] not null
	}
	return rows, total, nil
}
