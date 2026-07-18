//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/store"
)

// sharedDSN / sharedDB back every e2e test's data plane. A single embedded
// PostgreSQL is started once per package via TestMain (mirroring
// internal/store/store_dbtest_test.go), migrated once, and reused; each test
// resets state via truncateAll rather than paying the ~3-4s cost of spinning
// up a fresh instance. See design/e2e.md and the plan for the rationale.
var (
	sharedDSN string
	sharedDB  *store.DB
)

func TestMain(m *testing.M) {
	// Fixed port + RuntimePath so the binary/data dir is reused (avoids re-extract
	// per run) and does not clash with the store dbtest instance (54329) or a
	// local dev PostgreSQL (5432).
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(54330).
			Database("voxeltoad_e2e").
			RuntimePath(filepath.Join(os.TempDir(), "voxeltoad-epg-e2e-shared")),
	)
	if err := pg.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres start:", err)
		os.Exit(1)
	}
	sharedDSN = "postgres://postgres:postgres@localhost:54330/voxeltoad_e2e?sslmode=disable"

	db, err := store.Open(sharedDSN)
	if err != nil {
		_ = pg.Stop()
		fmt.Fprintln(os.Stderr, "open shared db:", err)
		os.Exit(1)
	}
	if err := store.Migrate(db); err != nil {
		_ = pg.Stop()
		fmt.Fprintln(os.Stderr, "migrate shared db:", err)
		os.Exit(1)
	}
	sharedDB = db

	code := m.Run()

	_ = db.Close()
	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "embedded-postgres stop:", err)
	}
	os.Exit(code)
}

// e2eTables lists every table seeded/written by the e2e stack, in an order safe
// for a single TRUNCATE ... CASCADE. Partitioned parents (usage_records,
// request_logs) cascade to their partitions automatically. config_generation is
// deliberately excluded: it is a single seed row (version 0) that config writes
// UPDATE in place — truncating it would drop the row so the version never bumps
// and snapshots come back empty. It is reset via UPDATE in truncateAll instead
// (mirroring internal/store/config_dbtest_test.go and admin/crud_dbtest_test.go).
// Kept in sync with internal/store/migrations/.
var e2eTables = []string{
	"providers",
	"models",
	"routes",
	"plugins",
	"tenants",
	"groups",
	"api_keys",
	"quotas",
	"usage_records",
	"audit_logs",
	"operators",
	"sessions",
	"request_logs",
}

// truncateAll wipes all e2e tables on the shared DB, restarting identity
// sequences so each test starts from a clean, deterministic state. CASCADE
// handles FK chains (tenants→groups→api_keys) and partitioned children. The
// config_generation counter is reset in place (see e2eTables note).
func truncateAll(t *testing.T) {
	t.Helper()
	stmt := "TRUNCATE " + strings.Join(e2eTables, ", ") + " RESTART IDENTITY CASCADE"
	if err := sharedDB.Exec(stmt).Error; err != nil {
		t.Fatalf("truncate e2e tables: %v", err)
	}
	if err := sharedDB.Exec(`UPDATE config_generation SET version = 0`).Error; err != nil {
		t.Fatalf("reset config_generation: %v", err)
	}
}
