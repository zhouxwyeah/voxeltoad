//go:build dbtest

// Package app_test exercises the data-plane store assembly against a real
// PostgreSQL (embedded). It shares the migration harness pattern with
// internal/store tests.
package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/app"
	"voxeltoad/internal/store"
)

var testDSN string

func TestMain(m *testing.M) {
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(54330). // distinct from internal/store's 54329
			Database("voxeltoad_app_test").
			RuntimePath(filepath.Join(os.TempDir(), "voxeltoad-epg-app")),
	)
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres start:", err)
		os.Exit(1)
	}
	testDSN = "postgres://postgres:postgres@localhost:54330/voxeltoad_app_test?sslmode=disable"
	code := m.Run()
	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres stop:", err)
	}
	os.Exit(code)
}

// OpenStores wires the PG-backed auth + quota + usage stores the data plane
// injects, against a migrated database.
func TestOpenStores_WiresPGBackends(t *testing.T) {
	ctx := context.Background()

	db, err := store.Open(testDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = db.Close()

	stores, err := app.OpenStores(testDSN, app.StoreOptions{UsageBuffer: 16})
	if err != nil {
		t.Fatalf("OpenStores: %v", err)
	}
	defer func() { _ = stores.Close() }()

	// Seed a tenant/group/key and a quota, then exercise each backend through
	// the interfaces the data plane uses.
	seed(t, stores, "key_app", "e"+repeat("0", 63), "acme-app", "team-app")

	// KeyStore: the seeded key resolves with tenant/group names.
	rec, ok, err := stores.KeyStore.LookupByHash(ctx, "e"+repeat("0", 63))
	if err != nil || !ok {
		t.Fatalf("LookupByHash = ok %v err %v", ok, err)
	}
	if rec.Tenant != "acme-app" || rec.KeyID != "key_app" {
		t.Errorf("record = %+v, want acme-app/key_app", rec)
	}

	// Quota: TryDebit/Settle against a configured scope.
	if err := stores.SetQuota(ctx, "tenant:acme-app", 1000, "usd"); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	okDebit, err := stores.Quota.TryDebit(ctx, []string{"tenant:acme-app"}, 400)
	if err != nil || !okDebit {
		t.Fatalf("TryDebit = %v,%v; want true,nil", okDebit, err)
	}

	// UsageRecorder: a record eventually lands in usage_records (async).
	if err := stores.UsageRecorder.Record(ctx, billingUsage()); err != nil {
		t.Fatalf("Record: %v", err)
	}
	waitFor(t, func() bool { return countUsage(t, stores) == 1 }, 2*time.Second)
}
