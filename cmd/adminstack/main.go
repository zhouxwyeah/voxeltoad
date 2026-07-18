//go:build adminstack

// Command adminstack brings up a real management-plane (admin) HTTP server
// backed by embedded PostgreSQL, for testing the Control Panel API against real
// admin workflows — including via the generated TypeScript admin client.
//
// It is NOT part of the normal build or CI: it is guarded by the `adminstack`
// build tag and pulls in the embedded-postgres test dependency. Run it with:
//
//	make adminstack          # pre-built binary + clean PG shutdown on Ctrl-C
//	./scripts/adminstack.sh   # same, directly
//
// Do NOT launch it via `go run ./cmd/adminstack` for manual testing: `go run`
// forks a child binary that orphans the embedded PostgreSQL when interrupted.
// `make start-stack` / `make adminstack-test` use the same pre-built-binary
// pattern via scripts/start-stack.sh and scripts/adminstack-test.sh.
//
// It starts embedded PostgreSQL (downloaded & cached on first run), applies
// migrations, bootstraps a super-admin, and serves the exact admin.Router the
// production admin plane uses (login, config CRUD, tenants, api-keys, quotas,
// usage, audit) on a fixed port with CORS enabled. It prints the base URL +
// credentials, then blocks until Ctrl-C, tearing everything down on exit.
//
// The companion script scripts/adminstack-test.sh boots this, runs the SDK
// cross-language contract test against it, and shuts it down.
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/store"
)

// Fixed local settings so the printed URLs/credentials are stable across runs.
// The PostgreSQL port is picked free at startup (see freePort) so a lingering
// embedded-postgres from a previously hard-killed run never causes a bind collision.
const (
	adminAddr     = "127.0.0.1:8090"
	adminOrigin   = "http://localhost:5173" // a typical Vite dev-server origin
	adminEmail    = "root@adminstack"
	adminPassword = "adminstack-pass-123"
)

func main() {
	fail := func(msg string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "adminstack: %s: %v\n", msg, err)
			os.Exit(1)
		}
	}

	// --- PostgreSQL ---
	// Default: embedded PostgreSQL (no docker). The port is picked free at
	// startup (see freePort) so a lingering embedded-postgres from a previously
	// hard-killed run never causes a bind collision.
	//
	// Shared-PG mode: when GATEWAY_PG_DSN is set (scripts/stack-test-all.sh), skip
	// the embedded PG entirely and use that DSN — this lets the three stack
	// tests share ONE embedded PG (see cmd/testpg). Persistence is irrelevant
	// in that mode (the shared PG is ephemeral per `make ci` run).
	var dsn string
	persist := false
	if shared := os.Getenv("GATEWAY_PG_DSN"); shared != "" {
		fmt.Println("adminstack: using shared PostgreSQL via GATEWAY_PG_DSN")
		dsn = shared
	} else {
		pgPort := freePort()
		// Persistence: GATEWAY_PERSIST_DATA=1 keeps a stable data dir across restarts so
		// config/tenants/keys survive Ctrl-C. Default (unset/0) stays ephemeral: a
		// fresh temp dir per run, wiped on exit — the original behavior.
		persist = envBool("GATEWAY_PERSIST_DATA", false)
		cfg := embeddedpostgres.DefaultConfig().
			Port(uint32(pgPort)).
			Database("voxeltoad_adminstack")
		if persist {
			// IMPORTANT: embedded-postgres wipes RuntimePath on every Start() (it
			// re-extracts the binaries there), so the persistent data dir MUST live
			// outside it — otherwise it gets deleted and the cluster re-inits. Keep
			// the data dir as a sibling, which dataDirIsValid() then reuses.
			baseDir := filepath.Join(os.TempDir(), "voxeltoad-adminstack-pg")
			dataDir := filepath.Join(baseDir, "data")
			// RuntimePath is rebuilt each run under baseDir/runtime (disposable
			// binaries). DataPath persists across restarts.
			cfg = cfg.RuntimePath(filepath.Join(baseDir, "runtime")).DataPath(dataDir)
			fmt.Printf("adminstack: persistence ENABLED — data dir %s (rm -rf it to reset)\n", dataDir)
		} else {
			runtimeDir := filepath.Join(os.TempDir(), fmt.Sprintf("voxeltoad-adminstack-pg-%d", pgPort))
			_ = os.RemoveAll(runtimeDir)
			defer func() { _ = os.RemoveAll(runtimeDir) }()
			cfg = cfg.RuntimePath(runtimeDir)
		}
		pg := embeddedpostgres.NewDatabase(cfg)
		fmt.Println("adminstack: starting embedded PostgreSQL (first run downloads a PG binary)...")
		fail("start embedded postgres", pg.Start())
		defer func() { _ = pg.Stop() }()
		dsn = fmt.Sprintf("postgres://postgres:postgres@localhost:%d/voxeltoad_adminstack?sslmode=disable", pgPort)
	}

	db, err := store.Open(dsn)
	fail("open db", err)
	defer func() { _ = db.Close() }()
	fail("migrate", store.Migrate(db))

	// --- bootstrap the first super-admin (ADR-0017 §4) ---
	created, err := admin.Bootstrap(context.Background(), db, adminEmail, adminPassword)
	fail("bootstrap super-admin", err)
	if !created {
		fmt.Println("adminstack: super-admin already existed (no-op)")
	}

	// --- provider credential encryption (dev-only fixed key) ---
	kek := sha256.Sum256([]byte("adminstack-dev-provider-credential-kek"))
	credService, err := credential.NewAESGCMService(kek[:])
	fail("create credential service", err)
	credRepo := store.NewCredentialRepo(db)

	// --- demo data (providers/models/routes/tenant/api-key/operator/quota) ---
	// GATEWAY_SEED_DEMO=0 disables. Idempotent (all store upserts use ON CONFLICT),
	// so it is safe whether or not persistence is on. Errors are non-fatal: a
	// broken seed must not block local development.
	seeded := ""
	if envBool("GATEWAY_SEED_DEMO", true) {
		fmt.Println("adminstack: seeding demo data...")
		info, sErr := seedDemoData(context.Background(), db, credService)
		if sErr != nil {
			fmt.Fprintf(os.Stderr, "adminstack: demo seed (non-fatal): %v\n", sErr)
		} else {
			seeded = info
			fmt.Println("adminstack: demo data ready")
		}
	}

	// --- real admin HTTP server (same Router as production) ---
	srv := &http.Server{
		Addr: adminAddr,
		Handler: admin.Router(admin.Options{
			DB:                db,
			AllowedOrigins:    []string{adminOrigin},
			CredentialService: credService,
			CredentialRepo:    credRepo,
		}),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fail("serve", err)
		}
	}()

	fmt.Printf(`
adminstack ready ✅
  admin API   http://%s
  super-admin %s / %s
  CORS origin %s
  postgres    %s%s
  persistence %s (GATEWAY_PERSIST_DATA)

Test it:
  VOXELTOAD_ADMIN_E2E=1 \
  VOXELTOAD_ADMIN_BASE_URL=http://%s \
  VOXELTOAD_ADMIN_EMAIL=%s VOXELTOAD_ADMIN_PASSWORD=%s \
  npm --prefix sdk/typescript run test:e2e

  # or the one-shot script:
  ./scripts/adminstack-test.sh

Ctrl-C to stop.
`, adminAddr, adminEmail, adminPassword, adminOrigin, dsn, seeded, persistLabel(persist),
		adminAddr, adminEmail, adminPassword)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\nadminstack: shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	// pg.Stop + runtimeDir cleanup run via defer (LIFO) on return.
}

// freePort asks the OS for an unused TCP port (mirrors devstack / the e2e
// harness). A fresh port each run avoids collisions with a lingering
// embedded-postgres.
func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "adminstack: free port: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// envBool reads a boolean env var. Unset → def. "1","true","yes" (any case) are
// truthy; everything else is false. Used for the GATEWAY_PERSIST_DATA and
// GATEWAY_SEED_DEMO switches.
func envBool(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// persistLabel renders the persistence state for the ready banner.
func persistLabel(on bool) string {
	if on {
		return "ON"
	}
	return "off (ephemeral)"
}
