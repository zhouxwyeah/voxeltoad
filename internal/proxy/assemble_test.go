package proxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"voxeltoad/internal/adapter"
	"voxeltoad/internal/config"
	"voxeltoad/internal/proxy"
)

// BuildDispatcher assembles a working Dispatcher from dynamic config: it
// resolves each provider's secret, constructs its adapter from the registry,
// builds a Forwarder, and wires routes + model preparation.
func TestBuildDispatcher_RoutesToProvider(t *testing.T) {
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(okBody))
	}))
	defer up.Close()

	dyn := &config.Dynamic{
		Providers: []config.Provider{{
			Name: "openai-prod", Type: "openai", Adapter: "openai",
			BaseURL: up.URL, APIKeyRef: "plain://sk-test",
			Timeouts: config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second},
		}},
		Models: []config.Model{{
			Alias:     "chat",
			Upstreams: []config.ModelUpstream{{Provider: "openai-prod", UpstreamModel: "gpt-4o"}},
		}},
		Routes: []config.Route{{
			ModelAlias: "chat",
			Providers:  []config.RouteProvider{{Name: "openai-prod"}},
			Strategy:   "priority",
		}},
	}

	disp, err := proxy.BuildDispatcher(dyn, proxy.DispatcherConfig{})
	if err != nil {
		t.Fatalf("BuildDispatcher: %v", err)
	}

	resp, dr, err := disp.Forward(context.Background(), "chat", &adapter.UnifiedRequest{
		Model:    "chat",
		Messages: []adapter.Message{{Role: "user", Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if dr.Provider != "openai-prod" {
		t.Errorf("provider = %q, want openai-prod", dr.Provider)
	}
	if dr.Fallback {
		t.Error("Fallback = true, want false (first candidate hit)")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 2 {
		t.Errorf("usage = %+v, want total 2", resp.Usage)
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d, want 1", hits)
	}
}

// Failover: the first provider 500s (retryable), the dispatcher fails over to
// the second and reports it as the hit provider.
func TestBuildDispatcher_FailsOver(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(okBody))
	}))
	defer good.Close()

	tmo := config.ProviderTimeouts{Connect: 2 * time.Second, FirstByte: 2 * time.Second, Overall: 5 * time.Second}
	dyn := &config.Dynamic{
		Providers: []config.Provider{
			{Name: "p-bad", Type: "openai", Adapter: "openai", BaseURL: bad.URL, APIKeyRef: "plain://k", Timeouts: tmo},
			{Name: "p-good", Type: "openai", Adapter: "openai", BaseURL: good.URL, APIKeyRef: "plain://k", Timeouts: tmo},
		},
		Models: []config.Model{{
			Alias: "chat",
			Upstreams: []config.ModelUpstream{
				{Provider: "p-bad", UpstreamModel: "gpt-4o"},
				{Provider: "p-good", UpstreamModel: "gpt-4o"},
			},
		}},
		Routes: []config.Route{{
			ModelAlias: "chat",
			Providers:  []config.RouteProvider{{Name: "p-bad"}, {Name: "p-good"}},
			Strategy:   "priority",
		}},
	}

	disp, err := proxy.BuildDispatcher(dyn, proxy.DispatcherConfig{})
	if err != nil {
		t.Fatalf("BuildDispatcher: %v", err)
	}
	_, dr, err := disp.Forward(context.Background(), "chat", &adapter.UnifiedRequest{
		Model: "chat", Messages: []adapter.Message{{Role: "user", Content: adapter.NewContentText("hi")}},
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if dr.Provider != "p-good" {
		t.Errorf("hit provider = %q, want p-good (failed over)", dr.Provider)
	}
	if !dr.Fallback {
		t.Error("Fallback = false, want true (first candidate failed)")
	}
	if dr.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", dr.RetryCount)
	}
}

// An unknown adapter name is a build error.
func TestBuildDispatcher_UnknownAdapter(t *testing.T) {
	dyn := &config.Dynamic{
		Providers: []config.Provider{{Name: "x", Adapter: "nope", BaseURL: "http://x", APIKeyRef: "plain://k"}},
	}
	if _, err := proxy.BuildDispatcher(dyn, proxy.DispatcherConfig{}); err == nil {
		t.Error("expected error for unknown adapter")
	}
}

// A bad secret reference is a build error.
func TestBuildDispatcher_BadSecret(t *testing.T) {
	dyn := &config.Dynamic{
		Providers: []config.Provider{{Name: "x", Adapter: "openai", BaseURL: "http://x", APIKeyRef: "env://__definitely_unset_var__"}},
	}
	if _, err := proxy.BuildDispatcher(dyn, proxy.DispatcherConfig{}); err == nil {
		t.Error("expected error for unresolvable secret")
	}
}
