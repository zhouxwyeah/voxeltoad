//go:build dbtest

// Package store_test holds repository tests that require a real PostgreSQL
// instance. They are isolated behind the `dbtest` build tag so the default
// `go test ./...` stays fast and dependency-free. Run with:
//
//	go test -tags=dbtest ./internal/store/...
//
// A single embedded-postgres instance is started per package via TestMain (see
// design/unit-test.md "embedded-postgres 设置").
package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/store"
)

// testDSN is the connection string for the package-shared embedded PostgreSQL.
// Repository tests open connections against it.
var testDSN string

func TestMain(m *testing.M) {
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(54329). // avoid the default 5432 to not clash with `make dev-deps`
			Database("voxeltoad_test").
			// Per-package RuntimePath so parallel dbtest packages (make test-db
			// runs them concurrently) don't contend on the shared default dir.
			RuntimePath(filepath.Join(os.TempDir(), "voxeltoad-epg-store")),
	)
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres start:", err)
		os.Exit(1)
	}
	testDSN = "postgres://postgres:postgres@localhost:54329/voxeltoad_test?sslmode=disable"

	code := m.Run()

	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres stop:", err)
	}
	os.Exit(code)
}

// TestEmbeddedPostgresConnects is a smoke test verifying the harness brings up
// a usable PostgreSQL.
func TestEmbeddedPostgresConnects(t *testing.T) {
	db, err := store.Open(testDSN)
	if err != nil {
		t.Fatalf("open embedded postgres: %v", err)
	}
	defer func() { _ = db.Close() }()

	var got int
	if err := db.Raw("SELECT 1").Scan(&got).Error; err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Errorf("SELECT 1 = %d, want 1", got)
	}
}
