// Command admin is the management-plane entrypoint. It exposes the admin REST
// API (providers, models, routes, API keys, quotas, plugins), persists to
// PostgreSQL, and serves the config snapshot the data plane polls.
//
// The admin plane is intentionally lightweight. See design/architecture.md.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voxeltoad/internal/admin"
	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/store"
)

// Build metadata, injected at build time via -ldflags "-X main.version=..."
// (see scripts/build.sh). Defaults apply for `go run` / plain `go build`.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", os.Getenv("ADMIN_CONFIG"), "path to bootstrap config YAML")
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	bootstrapOnly := flag.Bool("bootstrap", false, "create the first super-admin operator and exit (idempotent)")
	bootstrapEmail := flag.String("email", "", "super-admin email (with -bootstrap)")
	bootstrapPassword := flag.String("password", "", "super-admin password (with -bootstrap)")
	flag.Parse()

	log := observability.Logger()
	log.Info("starting admin", "version", version, "commit", commit, "date", date)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	// Migrations run on startup, guarded by a PG advisory lock so concurrent
	// admin instances do not race (ADR-0015). The `-migrate` flag applies them
	// and exits, for ops/manual control.
	db, err := store.Open(cfg.DB.DSN)
	if err != nil {
		log.Error("open database", "err", err)
		os.Exit(1)
	}
	if err := store.Migrate(db); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	if *migrateOnly {
		_ = db.Close()
		log.Info("migrations applied")
		return
	}

	// Bootstrap the first super-admin (ADR-0017 §4): idempotent, no-op if one
	// already exists. No credentials in VCS, no default account.
	if *bootstrapOnly {
		created, err := admin.Bootstrap(context.Background(), db, *bootstrapEmail, *bootstrapPassword)
		if err != nil {
			log.Error("bootstrap", "err", err)
			_ = db.Close()
			os.Exit(1)
		}
		_ = db.Close()
		if created {
			log.Info("super-admin created", "email", *bootstrapEmail)
		} else {
			log.Info("bootstrap no-op: a super-admin already exists")
		}
		return
	}

	var internalToken string
	if ref := cfg.Snapshot.InternalTokenRef; ref != "" {
		if internalToken, err = config.ResolveSecret(ref); err != nil {
			log.Error("resolve internal token", "err", err)
			os.Exit(1)
		}
	}

	var credService credential.Service
	var credRepo *store.CredentialRepo
	if kekRef := cfg.Gateway.ProviderCredentialKEKRef; kekRef != "" {
		kekRaw, err := config.ResolveSecret(kekRef)
		if err != nil {
			log.Error("resolve provider credential KEK", "err", err)
			os.Exit(1)
		}
		kek, err := credential.DecodeBase64Key(kekRaw)
		if err != nil {
			log.Error("decode provider credential KEK", "err", err)
			os.Exit(1)
		}
		credService, err = credential.NewAESGCMService(kek)
		if err != nil {
			log.Error("create credential service", "err", err)
			os.Exit(1)
		}
		credRepo = store.NewCredentialRepo(db)
		log.Info("provider credential encryption enabled", "algorithm", credService.Algorithm())
	}

	srv := &http.Server{
		Addr: cfg.Admin.Addr,
		Handler: admin.Router(admin.Options{
			InternalToken:     internalToken,
			DB:                db,
			AllowedOrigins:    cfg.Admin.AllowedOrigins,
			CredentialService: credService,
			CredentialRepo:    credRepo,
		}),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("admin listening", "addr", cfg.Admin.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	// Stale node cleanup: every 60s, mark instances as offline whose heartbeat
	// is older than 3× the heartbeat interval (15s → 45s threshold).
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	go func() {
		dpRepo := store.NewDataPlaneRepo(db)
		tick := time.NewTicker(60 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-tick.C:
				n, err := dpRepo.CleanupStale(context.Background(), 45*time.Second)
				if err != nil {
					log.Warn("stale node cleanup failed", "err", err)
				} else if n > 0 {
					log.Info("stale nodes cleaned up", "count", n)
				}
			}
		}
	}()

	// Trace-payload retention (ADR-0039 §4 / Phase 4): drop monthly
	// trace_payloads partitions older than the retention window. Runs once on
	// startup and then daily; a no-op when capture is disabled (there is simply
	// nothing to drop). Partition-DROP is O(1) per expired month and avoids the
	// bloat of a DELETE scan over large JSONB rows.
	//
	// The retention window is read from the hot-reloadable GatewaySettings on
	// each run (so changing it in the admin UI takes effect on the next daily
	// run), falling back to the bootstrap default when settings is unset/<=0.
	bootstrapRetention := cfg.Trace.CapturePayload.RetentionDays
	if bootstrapRetention <= 0 {
		bootstrapRetention = 7
	}
	go func() {
		repo := store.NewTracePayloadRepo(db)
		cfgRepo := store.NewConfigRepo(db, nil)
		drop := func() {
			retentionDays := bootstrapRetention
			if s, err := cfgRepo.GetSettings(context.Background()); err != nil {
				log.Warn("trace retention: read settings failed; using bootstrap default", "err", err)
			} else if s.Trace.RetentionDays > 0 {
				retentionDays = s.Trace.RetentionDays
			}
			cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
			n, err := repo.DropTracePayloadPartitionsBefore(context.Background(), cutoff)
			if err != nil {
				log.Warn("trace payload retention drop failed", "err", err)
			} else if n > 0 {
				log.Info("trace payload partitions dropped", "count", n, "retention_days", retentionDays)
			}
		}
		drop()
		tick := time.NewTicker(24 * time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-tick.C:
				drop()
			}
		}
	}()

	<-stop

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("graceful shutdown failed", "err", err)
	}
	_ = db.Close()
	log.Info("admin stopped")
}
