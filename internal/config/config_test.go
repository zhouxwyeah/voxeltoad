package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoad_EmptyPathReturnsDefault(t *testing.T) {
	got, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("Load(\"\") = %+v, want Default() %+v", got, Default())
	}
}

func TestLoad_MissingFileReturnsError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_PartialYAMLMergesOverDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.yaml")
	// Only override the gateway addr and the snapshot poll interval; everything
	// else must retain Default() values.
	yaml := "" +
		"gateway:\n" +
		"  addr: \":19090\"\n" +
		"snapshot:\n" +
		"  poll_interval: 2s\n" +
		"  internal_token_ref: \"plain://test-token\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	def := Default()
	if got.Gateway.Addr != ":19090" {
		t.Errorf("Gateway.Addr = %q, want \":19090\"", got.Gateway.Addr)
	}
	if got.Snapshot.PollInterval != 2*time.Second {
		t.Errorf("Snapshot.PollInterval = %v, want 2s", got.Snapshot.PollInterval)
	}
	// Untouched fields keep defaults.
	if got.Admin.Addr != def.Admin.Addr {
		t.Errorf("Admin.Addr = %q, want default %q", got.Admin.Addr, def.Admin.Addr)
	}
	if got.DB.DSN != def.DB.DSN {
		t.Errorf("DB.DSN = %q, want default %q", got.DB.DSN, def.DB.DSN)
	}
	if got.Snapshot.AdminURL != def.Snapshot.AdminURL {
		t.Errorf("Snapshot.AdminURL = %q, want default %q", got.Snapshot.AdminURL, def.Snapshot.AdminURL)
	}
}

func TestLoad_RateLimitParsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.yaml")
	yaml := "" +
		"gateway:\n" +
		"  rate_limit:\n" +
		"    tenant_rpm: 60\n" +
		"    tenant_tpm: 100000\n" +
		"    key_rpm: 10\n" +
		"    window: 30s\n" +
		"snapshot:\n" +
		"  internal_token_ref: \"plain://test-token\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rl := got.Gateway.RateLimit
	if rl.TenantRPM != 60 || rl.TenantTPM != 100000 || rl.KeyRPM != 10 {
		t.Errorf("rate_limit = %+v, want tenant_rpm 60 / tenant_tpm 100000 / key_rpm 10", rl)
	}
	if rl.Window != 30*time.Second {
		t.Errorf("rate_limit.window = %v, want 30s", rl.Window)
	}
	// Unset dimensions stay zero (= no limit).
	if rl.GroupRPM != 0 || rl.KeyTPM != 0 {
		t.Errorf("unset dims should be 0: %+v", rl)
	}
}

func TestStore_ConcurrentSetCurrent(t *testing.T) {
	s := NewStore()
	if s.Current() == nil {
		t.Fatal("NewStore Current() should not be nil")
	}

	var wg sync.WaitGroup
	var reads atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			s.set(&Dynamic{Version: string(rune('a' + n))})
		}(i)
		go func() {
			defer wg.Done()
			if s.Current() != nil {
				reads.Add(1)
			}
		}()
	}
	wg.Wait()

	if s.Current() == nil {
		t.Error("Current() nil after concurrent writes")
	}
}

func TestPollerFetch_OKParsesAndStores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", "v1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v1","raw":null}`))
	}))
	defer srv.Close()

	store := NewStore()
	p := NewPoller(srv.URL, time.Second, store)
	if err := p.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := store.Current().Version; got != "v1" {
		t.Errorf("Version = %q, want v1", got)
	}
}

func TestPollerFetch_ETagFallbackWhenBodyVersionEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", "etag-2")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"raw":null}`)) // no version in body
	}))
	defer srv.Close()

	store := NewStore()
	p := NewPoller(srv.URL, time.Second, store)
	if err := p.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := store.Current().Version; got != "etag-2" {
		t.Errorf("Version = %q, want etag-2 (from ETag header)", got)
	}
}

func TestPollerFetch_NotModifiedKeepsCurrent(t *testing.T) {
	var sentINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentINM = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	store := NewStore()
	store.set(&Dynamic{Version: "v5"})
	p := NewPoller(srv.URL, time.Second, store)

	if err := p.fetch(context.Background()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if sentINM != "v5" {
		t.Errorf("If-None-Match sent = %q, want v5", sentINM)
	}
	if got := store.Current().Version; got != "v5" {
		t.Errorf("Version = %q, want unchanged v5", got)
	}
}

func TestPollerFetch_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewPoller(srv.URL, time.Second, NewStore())
	if err := p.fetch(context.Background()); err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}
