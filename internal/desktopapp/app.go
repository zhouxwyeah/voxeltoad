// Package desktopapp is the shared application logic for the desktop personal
// gateway. It is consumed by both the CLI/dev entrypoint (cmd/desktop) and the
// Wails .app entrypoint (deploy/desktop).
//
// It reuses the enterprise data plane (internal/proxy, adapter, plugin,
// observability, auth, config) verbatim and replaces only the PostgreSQL-backed
// store and the admin-snapshot config poller with local SQLite + a local YAML
// file. No admin/RBAC/operator/billing is built — the default seeded key grants
// unrestricted model access, so the data plane's single permission gate
// (AllowedModels) passes for all requests. See design/desktop.md.
package desktopapp

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"voxeltoad/cmd/desktop/config"
	"voxeltoad/cmd/desktop/seed"
	"voxeltoad/internal/app"
	"voxeltoad/internal/auth"
	cfg "voxeltoad/internal/config"
	"voxeltoad/internal/desktopapi"
	"voxeltoad/internal/desktopstore"
	"voxeltoad/internal/observability"
	"voxeltoad/internal/plugin"
	"voxeltoad/internal/proxy"
)

// Main is the shared entrypoint for all desktop gateway binaries. Callers
// (cmd/desktop and deploy/desktop) provide only a thin main() wrapper.
func Main() {
	// The desktop gateway is inherently a local single-user tool; permit an
	// empty admin-snapshot token (ADR-0007 dev escape hatch) so the reused
	// config.Load validation passes without a snapshot channel.
	_ = os.Setenv("GATEWAY_ALLOW_INSECURE_DEV", "1")

	cfgPath := flag.String("config", envOr("DESKTOP_CONFIG", "desktop.yaml"), "path to desktop gateway YAML (gateway + dynamic sections)")
	dbPath := flag.String("db", envOr("DESKTOP_DB", "desktop.db"), "path to SQLite database file")
	webDist := flag.String("web-dist", envOr("DESKTOP_WEB_DIST", "desktop-ui/dist"), "path to built desktop UI (Vite dist); empty disables the UI")
	flag.Parse()

	log.Println("starting desktop gateway")

	// Ensure a usable dynamic config exists on first run.
	if err := seed.EnsureTemplate(*cfgPath); err != nil {
		log.Fatalf("ensure config template: %v", err)
	}

	// Bootstrap (gateway addr, session headers) — reused enterprise config.Load.
	bcfg, err := cfg.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load bootstrap config: %v", err)
	}

	// Open SQLite and run schema migration.
	db, err := desktopstore.Open(*dbPath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// Seed the single default API key (K1) and log its plaintext for agents.
	plaintextKey := seed.DefaultKey()
	if err := seed.Key(context.Background(), db, plaintextKey); err != nil {
		log.Fatalf("seed default key: %v", err)
	}
	log.Printf("desktop gateway API key: %s", plaintextKey)

	// Dynamic config closure — replaces the enterprise admin-snapshot poller.
	dynFn, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load dynamic config: %v", err)
	}
	settingsFn := func() *cfg.GatewaySettings {
		d := dynFn()
		if d == nil || d.Settings == nil {
			return &cfg.GatewaySettings{}
		}
		return d.Settings
	}

	// Auth over the SQLite KeyStore.
	authn := auth.NewAuthenticator(desktopstore.NewKeyStore(db), auth.Options{})

	// Dispatcher built from the local dynamic config (reused enterprise watcher).
	dispWatcher := app.NewDispatcherWatcher(dynFn, proxy.DispatcherConfig{})
	if err := dispWatcher.Build(); err != nil {
		log.Printf("warn: initial dispatcher build failed (chat unavailable until config is valid): %v", err)
	}

	// Async recorders over SQLite sinks. Trace capture is gated per-request by
	// GatewaySettings (enabled in the default template).
	reqRec := observability.NewAsyncRequestLogRecorder(desktopstore.NewRequestLogSink(db), 1024)
	reqRec.Start()
	defer reqRec.Close()
	traceRec := observability.NewAsyncTracePayloadRecorder(desktopstore.NewTracePayloadSink(db), 1024)
	traceRec.Start()
	defer traceRec.Close()

	proxyRouter := proxy.Router(nil,
		proxy.WithAuth(authn),
		proxy.WithPlugins(plugin.NewChain()),
		proxy.WithDispatcherProvider(dispWatcher.Current),
		proxy.WithSessionHeaders(bcfg.Gateway.SessionHeaders...),
		proxy.WithAuditRecorder(reqRec),
		proxy.WithTracePayloadRecorder(traceRec),
		proxy.WithSettingsSource(settingsFn),
		proxy.WithAccessLog(),
	)

	// Thin read API (design/desktop.md §10.2) + the built SPA are mounted
	// alongside the data plane on the same port. Routing: /api/v1/* → read API,
	// /v1/* → data plane (agents' base_url), everything else → SPA. The Wails
	// (or browser) frontend calls the read API on the same origin.
	apiHandler := desktopapi.New(db, *cfgPath, dispWatcher).Handler()
	staticHandler := desktopapi.Static(*webDist)
	rootHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/"):
			apiHandler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/v1/"):
			proxyRouter.ServeHTTP(w, r)
		default:
			staticHandler.ServeHTTP(w, r)
		}
	})

	srv := &http.Server{
		Addr:    bcfg.Gateway.Addr,
		Handler: rootHandler,
		// WriteTimeout intentionally 0: streaming responses rely on per-stage
		// timeouts (see design/e2e.md Pitfalls).
		ReadTimeout: 30 * time.Second,
	}

	// runMain blocks the main thread until the process should exit. The
	// implementation is build-tag split:
	//   - default (!desktop): HTTP server in a goroutine + signal.Notify wait
	//     (run_cli.go). The CLI/dev mode used by `make desktop-web-dev`.
	//   - desktop tag: Wails app window (run_desktop.go), with the HTTP server
	//     started in Wails OnStartup and stopped in OnShutdown. Used by
	//     `make desktop-build` to produce the macOS .app.
	runMain(runMainDeps{
		srv:          srv,
		gatewayAddr:  bcfg.Gateway.Addr,
		onReload:     dispWatcher.Build,
		cfgPath:      *cfgPath,
		plaintextKey: plaintextKey,
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// runMainDeps is the parameter bag passed to the build-tag-split runMain.
type runMainDeps struct {
	srv          *http.Server
	gatewayAddr  string       // e.g. "127.0.0.1:8787" — for the Wails reverse proxy target
	onReload     func() error // dispWatcher.Build (hot-reload, design/desktop.md §7)
	cfgPath      string       // for the "Open config folder" Wails menu item
	plaintextKey string       // logged + exposed via the "Copy API key" menu item
}
