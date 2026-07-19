package desktopapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Without SetQuitFunc the endpoint is disabled (503) — the CLI/dev runner
// never injects a quit hook.
func TestAppQuit_NotConfigured(t *testing.T) {
	db := openTestDB(t)
	ts := httptest.NewServer(New(db, "", nil, nil, nil).Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/app/quit", "application/json", nil)
	if err != nil {
		t.Fatalf("POST quit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

// The quit callback must fire AFTER the 200 response lands (the handler
// defers it via a goroutine so quitting doesn't kill the response).
func TestAppQuit_InvokesCallback(t *testing.T) {
	db := openTestDB(t)
	srv := New(db, "", nil, nil, nil)
	quit := make(chan struct{})
	srv.SetQuitFunc(func() { close(quit) })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/app/quit", "application/json", nil)
	if err != nil {
		t.Fatalf("POST quit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Status != "quitting" {
		t.Fatalf("want status quitting, got %q", env.Data.Status)
	}

	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("quit callback not invoked within 2s")
	}
}
