package desktopapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The reveal endpoint guards configPath at request time (registered
// unconditionally so a config-less server answers a JSON 503, not a mux 404).
func TestConfigReveal_NoConfigPath(t *testing.T) {
	db := openTestDB(t)
	ts := httptest.NewServer(New(db, "", nil, nil, nil).Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/config/reveal", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reveal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}

// RevealConfigFile rejects an empty path before touching the OS (the platform
// exec branches are exercised manually — running them in tests would open
// file-manager windows on the dev machine).
func TestRevealConfigFile_EmptyPath(t *testing.T) {
	if err := RevealConfigFile(""); err == nil {
		t.Fatal("want error for empty path, got nil")
	}
}
