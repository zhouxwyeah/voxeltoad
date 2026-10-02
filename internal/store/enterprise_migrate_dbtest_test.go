//go:build dbtest

package store_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"voxeltoad/internal/store"
)

func TestEnterpriseMigration_RoundTripPreservesLegacyRows(t *testing.T) {
	admin, err := store.Open(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("enterprise_migration_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := store.Open(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		if err := admin.Exec("DROP DATABASE " + name).Error; err != nil {
			t.Errorf("drop owned test database: %v", err)
		}
	})
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tenants(name) VALUES('legacy'); INSERT INTO usage_records(tenant,api_key_id,provider,model,cost) VALUES('legacy','old-key','p','m',123)`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.Migrate(db); err != nil {
			t.Fatal(err)
		}
		var row struct {
			ApplicationID         *int64
			Environment, Currency string
			Cost                  int64
		}
		if err := db.Raw(`SELECT application_id,environment,currency,cost FROM usage_records WHERE tenant='legacy'`).Scan(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.ApplicationID != nil || row.Environment != "" || row.Currency != "" || row.Cost != 123 {
			t.Fatalf("legacy row rewritten: %+v", row)
		}
		for _, table := range []string{"applications", "budget_policies", "budget_accounts", "billing_reservations", "billing_reservation_items", "budget_events"} {
			var exists bool
			if err := db.Raw(`SELECT to_regclass(?) IS NOT NULL`, table).Scan(&exists).Error; err != nil || !exists {
				t.Fatalf("table %s: exists=%v err=%v", table, exists, err)
			}
		}
		if i == 0 {
			if _, err := p.DownTo(ctx, 27); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Raw(`SELECT count(*) FROM usage_records WHERE tenant='legacy' AND cost=123`).Scan(&count).Error; err != nil || count != 1 {
				t.Fatalf("rollback lost legacy data: %d %v", count, err)
			}
		}
	}
}
