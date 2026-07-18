package app_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"voxeltoad/internal/app"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

func dispDyn(version, upstreamURL string) *config.Dynamic {
	return &config.Dynamic{
		Version: version,
		Providers: []config.Provider{{
			Name: "p1", Type: "openai", Adapter: "openai",
			BaseURL: upstreamURL, APIKeyRef: "plain://k",
			Timeouts: config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second},
		}},
		Models: []config.Model{{Alias: "chat", Upstreams: []config.ModelUpstream{{Provider: "p1", UpstreamModel: "gpt-4o"}}}},
		Routes: []config.Route{{ModelAlias: "chat", Providers: []config.RouteProvider{{Name: "p1"}}, Strategy: "priority"}},
	}
}

// The dispatcher watcher builds a Dispatcher from the current config and rebuilds
// it (atomic swap) whenever the snapshot version changes.
func TestDispatcherWatcher_BuildsAndRebuilds(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()

	var cur atomic.Pointer[config.Dynamic]
	cur.Store(dispDyn("v1", up.URL))

	w := app.NewDispatcherWatcher(cur.Load, proxy.DispatcherConfig{})
	if err := w.Build(); err != nil { // initial synchronous build
		t.Fatalf("initial Build: %v", err)
	}
	d1 := w.Current()
	if d1 == nil {
		t.Fatal("expected a dispatcher after Build")
	}

	// Same version → Refresh is a no-op, same instance.
	w.Refresh()
	if w.Current() != d1 {
		t.Error("Refresh on unchanged version should keep the same dispatcher")
	}

	// New version → rebuild, different instance.
	cur.Store(dispDyn("v2", up.URL))
	w.Refresh()
	if w.Current() == d1 {
		t.Error("Refresh after version change should rebuild the dispatcher")
	}
}

// A build error (bad config) keeps the last-good dispatcher.
func TestDispatcherWatcher_KeepsLastGoodOnError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()

	var cur atomic.Pointer[config.Dynamic]
	cur.Store(dispDyn("v1", up.URL))
	w := app.NewDispatcherWatcher(cur.Load, proxy.DispatcherConfig{})
	if err := w.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	good := w.Current()

	// Swap in a broken config (unresolvable secret) at a new version.
	bad := dispDyn("v3", up.URL)
	bad.Providers[0].APIKeyRef = "env://__unset_var_xyz__"
	cur.Store(bad)
	w.Refresh()

	if w.Current() != good {
		t.Error("a failed rebuild must keep the last-good dispatcher")
	}
}
