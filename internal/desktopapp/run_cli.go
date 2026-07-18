//go:build !desktop && !bindings

// CLI / dev mode for desktopapp: HTTP server in a goroutine + SIGINT/SIGTERM
// wait. This is the default build (no tag) used by `make desktop-web-dev` and
// the dev workflow — no Wails/cgo dependency. The macOS .app build uses the
// desktop-tagged run_desktop.go instead.
//
// The `bindings` tag is excluded because Wails' binding generator needs to
// execute the Wails code path (run_desktop.go) to extract bindings.
package desktopapp

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func runMain(d runMainDeps) {
	go func() {
		log.Printf("desktop gateway listening on %s", d.gatewayAddr)
		if err := d.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.srv.Shutdown(shutCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("desktop gateway stopped")
}
