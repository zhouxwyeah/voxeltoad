// Command gateway is the data-plane entrypoint. It serves the OpenAI-compatible
// API, runs the plugin chain, resolves routes, and forwards (and streams)
// requests to upstream providers.
//
// The data plane pulls configuration by polling the admin plane's HTTP
// config-snapshot endpoint and rebuilds its dispatcher on every config change
// (hot reload, no restart). It additionally holds a direct connection to the
// quota store (its one synchronous stateful dependency; ADR-0013). Keys are
// looked up cache-first with a PG fallback (ADR-0006); usage is recorded
// asynchronously (ADR-0016). See design/architecture.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voxeltoad/internal/app"
	"voxeltoad/internal/auth"
	"voxeltoad/internal/billing"
	"voxeltoad/internal/config"
	"voxeltoad/internal/credential"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/plugin/ratelimit"
	"voxeltoad/internal/proxy"
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
	cfgPath := flag.String("config", os.Getenv("GATEWAY_CONFIG"), "path to bootstrap config YAML")
	flag.Parse()

	log := observability.Logger()
	log.Info("starting gateway", "version", version, "commit", commit, "date", date)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	shutdownOTel, err := observability.Init(ctx, observability.Provider{
		ServiceName: cfg.OTel.ServiceName,
		Endpoint:    cfg.OTel.Endpoint,
		Enabled:     cfg.OTel.Enabled,
	})
	if err != nil {
		log.Error("init observability", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownOTel(context.Background()) }()
	log.Info("otel initialized", "enabled", cfg.OTel.Enabled, "endpoint", cfg.OTel.Endpoint)

	// Start syncing dynamic config from the admin plane. The initial fetch is
	// synchronous so we fail fast if the admin plane is unreachable.
	cfgStore := config.NewStore()
	var pollerOpts []config.PollerOption
	if ref := cfg.Snapshot.InternalTokenRef; ref != "" {
		token, err := config.ResolveSecret(ref)
		if err != nil {
			log.Error("resolve internal token", "err", err)
			os.Exit(1)
		}
		pollerOpts = append(pollerOpts, config.WithInternalToken(token))
	}
	poller := config.NewPoller(cfg.Snapshot.AdminURL, cfg.Snapshot.PollInterval, cfgStore, pollerOpts...)
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	defer cancelPoll()
	if err := poller.Start(pollCtx); err != nil {
		log.Warn("config poller initial fetch failed; continuing with empty config", "err", err)
	}

	// Open the PG-backed data-plane stores (quota, keys, usage). Quota is the
	// data plane's one synchronous stateful dependency (ADR-0013); fail fast if
	// the database is unreachable.
	stores, err := app.OpenStores(cfg.DB.DSN, app.StoreOptions{
		TracePayloadBuffer: cfg.Trace.CapturePayload.Buffer,
	})
	if err != nil {
		log.Error("open stores", "err", err)
		os.Exit(1)
	}
	defer func() { _ = stores.Close() }()
	log.Info("stores opened") // trace capture state is hot-reloadable via GatewaySettings, not known at startup

	// Register the db://provider/<name> secret resolver for encrypted upstream
	// credentials stored in PostgreSQL (ADR-0031). If no KEK is configured, any
	// provider using a db:// reference will fail to resolve with a clear error
	// rather than being silently treated as a literal key.
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
		credService, err := credential.NewAESGCMService(kek)
		if err != nil {
			log.Error("create credential service", "err", err)
			os.Exit(1)
		}
		credRepo := store.NewCredentialRepo(stores.DB())
		config.RegisterSecretScheme("db", func(path string) (string, error) {
			name, ok := config.ParseDBProviderPath(path)
			if !ok {
				return "", fmt.Errorf("config: invalid db credential reference db://%s", path)
			}
			enc, ok, err := credRepo.Get(context.Background(), name)
			if err != nil {
				return "", fmt.Errorf("config: load credential for provider %q: %w", name, err)
			}
			if !ok {
				return "", fmt.Errorf("config: no encrypted credential found for provider %q", name)
			}
			plain, err := credService.Decrypt(enc)
			if err != nil {
				return "", fmt.Errorf("config: decrypt credential for provider %q: %w", name, err)
			}
			return plain, nil
		})
	}

	// Self-register this data-plane instance for cluster visibility (admin UI
	// data_plane_nodes page). Fail-open: if the table hasn't been created yet or
	// PG is temporarily unavailable the instance still serves requests.
	host, errHost := os.Hostname()
	if errHost != nil {
		host = "unknown"
	}
	instanceID := fmt.Sprintf("%s-%d", host, os.Getpid())
	dpRepo := store.NewDataPlaneRepo(stores.DB())
	node := &store.DataPlaneNode{
		InstanceID:       instanceID,
		Hostname:         host,
		Addr:             cfg.Gateway.Addr,
		Version:          version,
		Commit:           commit,
		ConfigGeneration: 0,
	}
	if err := dpRepo.Register(context.Background(), node); err != nil {
		log.Warn("data-plane self-registration failed (table may not exist yet); node list unavailable", "err", err, "instance_id", instanceID)
	} else {
		log.Info("data-plane instance registered", "instance_id", instanceID)
		// Periodic heartbeat (every 15s); graceful drain on shutdown.
		hbCtx, cancelHB := context.WithCancel(context.Background())
		defer cancelHB()
		go func() {
			tick := time.NewTicker(15 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-hbCtx.Done():
					_ = dpRepo.Drain(context.Background(), instanceID)
					log.Info("data-plane instance drained", "instance_id", instanceID)
					return
				case <-tick.C:
					if err := dpRepo.Heartbeat(context.Background(), instanceID); err != nil {
						log.Warn("heartbeat failed", "err", err)
					}
				}
			}
		}()
	}

	// Build the governance chain over the PG stores. Authentication resolves
	// keys cache-first with the PG KeyStore as fallback; billing pre-debits and
	// settles quota and records usage. Pricing is read from the live config
	// snapshot (cfgStore.Current) so it tracks config changes.
	authn := auth.NewAuthenticator(stores.KeyStore, auth.Options{})
	billingPlugin := billing.NewPlugin(cfgStore.Current, stores.Quota, stores.UsageRecorder,
		billing.WithMaxTokensCeiling(cfg.Gateway.MaxTokensCeiling))

	// Governance plugins in Pre order: rate limiting first (reject over-limit
	// callers before the quota pre-debit does any work), then billing. Rate
	// limiting is installed only when configured; its state is in-memory and
	// per-instance in P0 (ADR-0008).
	//
	// B1': when multiple data-plane instances are online, divide the aggregate
	// rate limits evenly among them so the combined limit approximates the
	// configured total (no-Redis interim plan, see Phase 4a review).
	var plugins []plugin.Plugin
	if rl := cfg.Gateway.RateLimit; rl.Enabled() {
		if n, err := dpRepo.OnlineCount(context.Background()); err == nil && n > 1 {
			log.Info("rate limit division by online instances", "instances", n)
			divide := func(v int) int { return (v + n - 1) / n } // ceil(a/b)
			rl.TenantRPM = divide(rl.TenantRPM)
			rl.TenantTPM = divide(rl.TenantTPM)
			rl.GroupRPM = divide(rl.GroupRPM)
			rl.GroupTPM = divide(rl.GroupTPM)
			rl.KeyRPM = divide(rl.KeyRPM)
			rl.KeyTPM = divide(rl.KeyTPM)
		}
		plugins = append(plugins, ratelimit.NewPlugin(ratelimit.NewMemoryLimiter(), ratelimit.Limits{
			TenantRPM: rl.TenantRPM, TenantTPM: rl.TenantTPM,
			GroupRPM: rl.GroupRPM, GroupTPM: rl.GroupTPM,
			KeyRPM: rl.KeyRPM, KeyTPM: rl.KeyTPM,
			Window: rl.Window,
		}))
		log.Info("rate limiting enabled", "config", rl)
	}
	plugins = append(plugins, billingPlugin)
	chain := plugin.NewChain(plugins...)
	log.Info("plugin chain assembled", "plugins", len(plugins))

	// Wire the readiness probe: config fetched (non-empty version) + DB ping.
	stores.SetConfigFreshness(func() bool {
		cur := cfgStore.Current()
		return cur != nil && cur.Version != ""
	})

	// Assemble the dispatcher from dynamic config and rebuild it on every
	// config-snapshot version change (hot reload; the router resolves it per
	// request via the watcher's Current). The initial build is best-effort: an
	// empty/invalid config just means chat returns 501 until config arrives.
	dispWatcher := app.NewDispatcherWatcher(cfgStore.Current, proxy.DispatcherConfig{})
	if err := dispWatcher.Build(); err != nil {
		log.Warn("initial dispatcher build failed; chat unavailable until config is valid", "err", err)
	} else {
		version := ""
		if cur := cfgStore.Current(); cur != nil {
			version = cur.Version
		}
		log.Info("dispatcher built", "version", version)
	}
	go dispWatcher.Watch(pollCtx, cfg.Snapshot.PollInterval)

	// B2': report per-instance circuit breaker state via the existing heartbeat
	// channel so the admin overview panel can show aggregated breaker health.
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for range tick.C {
			d := dispWatcher.Current()
			if d == nil {
				continue
			}
			states := d.BreakerStates()
			if len(states) > 0 {
				if err := dpRepo.UpdateBreakerStates(context.Background(), instanceID, states); err != nil {
					log.Warn("breaker state report failed", "err", err)
				}
			}
		}
	}()

	srv := &http.Server{
		Addr: cfg.Gateway.Addr,
		Handler: proxy.Router(nil,
			proxy.WithAuth(authn),
			proxy.WithPlugins(chain),
			proxy.WithDispatcherProvider(dispWatcher.Current),
			proxy.WithSessionHeaders(cfg.Gateway.SessionHeaders...),
			proxy.WithAuditRecorder(stores.RequestLog),
			proxy.WithTracePayloadRecorder(stores.TracePayload),
			proxy.WithSettingsSource(cfgStore.Settings),
			proxy.WithReadinessProbe(stores),
			proxy.WithAccessLog(),
		),
		ReadTimeout: 30 * time.Second,
		// WriteTimeout intentionally 0: streaming responses rely on
		// per-stage timeouts (see design/e2e.md Pitfalls).
		WriteTimeout: 0,
	}

	go func() {
		log.Info("gateway listening", "addr", cfg.Gateway.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("graceful shutdown failed", "err", err)
	}
	log.Info("gateway stopped")
}
