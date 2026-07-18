//go:build desktop || bindings

// Wails mode for desktopapp: launches the macOS app window via
// deploy/desktop/app.App.Run(). The HTTP server's ListenAndServe happens inside
// App.OnStartup (Wails owns the main thread via wails.Run), and Shutdown is
// wired to App.OnShutdown (the .app Quit path, since GUI apps don't receive
// SIGINT/SIGTERM).
//
// The `bindings` tag is included because Wails' binding generator compiles and
// runs this code path to extract TypeScript bindings from App.Reload() and
// other bound methods. Without it, `wails build` would fail to generate
// bindings (it would fall through to the CLI path and never call wails.Run).
//
// Build with: CGO_ENABLED=1 go build -tags desktop -o desktop-gateway ./cmd/desktop
// or via `make desktop-build` (which also bundles the SPA via //go:embed).
package desktopapp

import (
	"fmt"
	"log"
	"net/http"

	"voxeltoad/deploy/desktop/app"
)

func runMain(d runMainDeps) {
	// HTTP server is started by app.App.OnStartup (in a goroutine, since
	// wails.Run blocks the main thread). OnShutdown handles Shutdown.
	log.Printf("desktop gateway (.app mode) starting HTTP server on %s via Wails OnStartup", d.gatewayAddr)

	desktopApp := &app.App{
		HTTPServer: d.srv,
		GatewayURL: fmt.Sprintf("http://%s", d.gatewayAddr),
		OnReload:   d.onReload,
		CfgPath:    d.cfgPath,
	}
	if err := desktopApp.Run(); err != nil {
		log.Fatalf("desktop app: %v", err)
	}
	// suppress unused-import / unused-symbol warnings for http in this tag.
	_ = http.StatusContinue
}
