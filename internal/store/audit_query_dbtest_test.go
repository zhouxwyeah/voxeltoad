//go:build dbtest

package store_test

import (
	"context"
	"testing"
	"time"

	"voxeltoad/internal/store"
)

func freshAuditRepo(t *testing.T) *store.DB {
	t.Helper()
	db := mustMigratedDB(t)
	if err := db.Exec(`TRUNCATE audit_logs`).Error; err != nil {
		t.Fatalf("truncate audit_logs: %v", err)
	}
	return db
}

// insertAudit writes an audit row with an explicit tenant (nil → global) and
// created_at for deterministic ordering.
func insertAudit(t *testing.T, db *store.DB, action, resType, resID string, tenant *string, at time.Time) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO audit_logs (operator_id, action, resource_type, resource_id, tenant, after, created_at)
		 VALUES (NULL, ?, ?, ?, ?, NULL, ?)`,
		action, resType, resID, tenant, at,
	).Error; err != nil {
		t.Fatalf("insert audit: %v", err)
	}
}

func strptr(s string) *string { return &s }

// A tenant-scoped audit query returns only rows attributed to that tenant —
// including super-admin actions on it — and never another tenant's rows nor
// global (NULL-tenant) rows. The global query sees everything.
func TestAuditQuery_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	db := freshAuditRepo(t)
	now := time.Now().UTC()

	insertAudit(t, db, "create", "api_key", "k1", strptr("acme"), now)
	// A super-admin action ON acme (e.g. top-up) is attributed to acme.
	insertAudit(t, db, "create", "quota", "tenant:acme", strptr("acme"), now.Add(-time.Second))
	insertAudit(t, db, "create", "api_key", "k2", strptr("other"), now)
	insertAudit(t, db, "create", "provider", "p1", nil, now) // global

	scoped := store.NewAuditQueryRepo(db, "acme")
	rows, _, err := scoped.List(ctx, store.AuditFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List scoped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("acme audit rows = %d, want 2 (own + super-admin-on-acme)", len(rows))
	}
	for _, r := range rows {
		if r.Tenant == nil || *r.Tenant != "acme" {
			t.Errorf("scoped query leaked row with tenant %v", r.Tenant)
		}
	}

	global := store.NewAuditQueryRepo(db, "")
	all, _, err := global.List(ctx, store.AuditFilter{}, "", 100)
	if err != nil {
		t.Fatalf("List global: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("global audit rows = %d, want 4", len(all))
	}
}

// Filters narrow by resource_type and action.
func TestAuditQuery_Filters(t *testing.T) {
	ctx := context.Background()
	db := freshAuditRepo(t)
	now := time.Now().UTC()
	insertAudit(t, db, "create", "provider", "p1", nil, now)
	insertAudit(t, db, "delete", "provider", "p1", nil, now.Add(-time.Second))
	insertAudit(t, db, "create", "model", "m1", nil, now.Add(-2*time.Second))

	repo := store.NewAuditQueryRepo(db, "")
	rows, _, err := repo.List(ctx, store.AuditFilter{ResourceType: "provider", Action: "create"}, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ResourceType != "provider" || rows[0].Action != "create" {
		t.Errorf("filtered rows = %+v, want one create/provider", rows)
	}
}

// Keyset pagination walks all rows with no gaps/dupes.
func TestAuditQuery_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	db := freshAuditRepo(t)
	base := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		insertAudit(t, db, "create", "provider", "p", nil, base.Add(-time.Duration(i)*time.Second))
	}

	repo := store.NewAuditQueryRepo(db, "")
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		rows, next, err := repo.List(ctx, store.AuditFilter{}, cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Errorf("row id %d returned twice", r.ID)
			}
			seen[r.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		cursor = next
		if pages > total+2 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Errorf("saw %d distinct rows, want %d", len(seen), total)
	}
}

// ListPage returns the requested offset window plus the total count, in
// (created_at, id) DESC order. An out-of-range page yields an empty slice but
// the total is still correct.
func TestAuditQuery_ListPage(t *testing.T) {
	ctx := context.Background()
	db := freshAuditRepo(t)
	base := time.Now().UTC()
	const total = 5
	for i := 0; i < total; i++ {
		insertAudit(t, db, "create", "provider", "p", nil, base.Add(-time.Duration(i)*time.Second))
	}

	repo := store.NewAuditQueryRepo(db, "")

	// Page 1, size 2 → newest two rows, total reflects the full set.
	rows, n, err := repo.ListPage(ctx, store.AuditFilter{}, 1, 2)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if n != total {
		t.Errorf("total = %d, want %d", n, total)
	}
	if len(rows) != 2 {
		t.Errorf("page 1 rows = %d, want 2", len(rows))
	}

	// Ordering is DESC by created_at; rows[0] is the newest.
	if !rows[0].CreatedAt.After(rows[1].CreatedAt) {
		t.Errorf("page not in DESC order: %v then %v", rows[0].CreatedAt, rows[1].CreatedAt)
	}

	// Page beyond the end → empty rows, total unchanged.
	over, n2, err := repo.ListPage(ctx, store.AuditFilter{}, 10, 2)
	if err != nil {
		t.Fatalf("ListPage overflow: %v", err)
	}
	if n2 != total {
		t.Errorf("overflow total = %d, want %d", n2, total)
	}
	if len(over) != 0 {
		t.Errorf("overflow rows = %d, want 0", len(over))
	}

	// The full set is covered without gaps/dupes when paging through.
	seen := map[int64]bool{}
	for p := 1; ; p++ {
		page, _, err := repo.ListPage(ctx, store.AuditFilter{}, p, 2)
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		if len(page) == 0 {
			break
		}
		for _, r := range page {
			if seen[r.ID] {
				t.Errorf("row %d duplicated across pages", r.ID)
			}
			seen[r.ID] = true
		}
	}
	if len(seen) != total {
		t.Errorf("paged through %d distinct rows, want %d", len(seen), total)
	}
}
