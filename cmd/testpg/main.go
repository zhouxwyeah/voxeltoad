//go:build testpg

// Command testpg starts ONE shared embedded PostgreSQL for the three stack
// tests (devstack-test / sdk-chat-e2e / adminstack-test). It is NOT part of
// the normal build or CI: it is guarded by the `testpg` build tag. Used by
// scripts/stack-test-all.sh, which runs it in the background, then starts
// devstack and adminstack against it via GATEWAY_PG_DSN (see those mains), runs
// the three test suites, and tears everything down. Run it via:
//
//	make stack-test-all   # one PG + three suites, one build each
//	./scripts/stack-test-all.sh
//
// Why this exists: each stack test previously booted its own embedded PG
// (~3-4s each) and rebuilt the same binaries, adding ~30-60s to `make ci`.
// Sharing one PG across the three suites removes the repeated initdb/boot.
//
// It starts embedded PostgreSQL on a FIXED port (54331) with a shared
// RuntimePath (reused across runs so the PG binaries are extracted once),
// applies migrations once against the default database, then creates the two
// per-stack databases (voxeltoad_devstack / voxeltoad_adminstack) that devstack and
// adminstack connect to. Keeping them in separate databases (not separate PG
// instances) gives schema isolation without paying two initdb+ boot costs.
//
// The databases are DROPPED+RECREATED on each run so repeated `make ci` runs
// always start from a clean slate even if a previous run was hard-killed.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver for database/sql
)

const (
	// Fixed port so stack-test-all.sh can health-check with pg_isready and so
	// repeated runs reuse the same RuntimePath. 55431 avoids the e2e harness
	// (54330), the store dbtest (54329), any local dev PG (5432), and the
	// 543x1 ephemeral ports some local apps (e.g. security agents) bind.
	pgPort = 55431
)

func main() {
	fail := func(msg string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "testpg: %s: %v\n", msg, err)
			os.Exit(1)
		}
	}

	runtimeDir := filepath.Join(os.TempDir(), "voxeltoad-epg-stack-shared")
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(pgPort).
			Database("postgres").
			RuntimePath(runtimeDir),
	)
	fmt.Println("testpg: starting shared embedded PostgreSQL (first run downloads a PG binary)...")
	fail("start embedded postgres", pg.Start())
	defer func() { _ = pg.Stop() }()

	// The stacks each run their own migrations on startup against their own
	// database, so we only need the two per-stack databases to EXIST — we do
	// not pre-migrate them here. Recreate them so repeated runs are clean.
	// DROP cannot run inside a transaction; the stacks are not running yet so
	// nothing is connected to them.
	baseDSN := fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", pgPort)
	sqlDB, err := sql.Open("pgx", baseDSN)
	fail("open base db", err)
	for _, name := range []string{"voxeltoad_devstack", "voxeltoad_adminstack"} {
		_, _ = sqlDB.Exec("DROP DATABASE IF EXISTS " + name)
		_, err := sqlDB.Exec("CREATE DATABASE " + name)
		fail("create "+name, err)
	}
	_ = sqlDB.Close()

	fmt.Printf(`testpg ready ✅
  devstack DSN    postgres://postgres:postgres@localhost:%d/voxeltoad_devstack?sslmode=disable
  adminstack DSN  postgres://postgres:postgres@localhost:%d/voxeltoad_adminstack?sslmode=disable

Ctrl-C (or the stack-test-all.sh wrapper) stops it.
`, pgPort, pgPort)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\ntestpg: shutting down...")
}
