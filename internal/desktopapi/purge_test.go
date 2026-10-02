package desktopapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	desktopcfg "voxeltoad/cmd/desktop/config"
	"voxeltoad/internal/app"
	"voxeltoad/internal/desktopstore"
	"voxeltoad/internal/proxy"
)

// TestPurgeObservation_EmptyBodyClearsAll verifies that an empty POST body
// is treated as "clear all" (not as a decode error that accidentally clears
// everything via a swallowed readJSON return). Regression for the critical
// bug where _ = readJSON(w, r, &payload) ignored the false return and
// proceeded to ClearAllObservation after readJSON had already written 400.
func TestPurgeObservation_EmptyBodyClearsAll(t *testing.T) {
	ts, db := newPurgeTestServer(t)

	seedRequestLog(t, db, "req-1", time.Now().Add(-1*time.Hour))
	seedRequestLog(t, db, "req-2", time.Now().Add(-30*time.Minute))

	// Confirm 2 rows exist via the overview endpoint.
	if n := overviewTotal(t, ts); n != 2 {
		t.Fatalf("pre-purge request count = %d, want 2", n)
	}

	// POST with nil body (no Content-Type) → should clear all, return 200.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/observation/purge", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("purge empty body: status %d, want 200: %s", resp.StatusCode, body)
	}
	var result struct {
		Deleted int `json:"deleted"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", result.Deleted)
	}
	if n := overviewTotal(t, ts); n != 0 {
		t.Errorf("post-purge request count = %d, want 0", n)
	}
}

// TestPurgeObservation_InvalidJSONReturns400 verifies that invalid JSON does
// NOT accidentally clear all data. Before the fix, readJSON wrote 400 but the
// code continued and called ClearAllObservation.
func TestPurgeObservation_InvalidJSONReturns400(t *testing.T) {
	ts, db := newPurgeTestServer(t)
	seedRequestLog(t, db, "req-survivor", time.Now().Add(-1*time.Hour))

	if n := overviewTotal(t, ts); n != 1 {
		t.Fatalf("pre-purge request count = %d, want 1", n)
	}

	// POST with invalid JSON → should get 400, data must survive.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/observation/purge", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid JSON: status %d, want 400", resp.StatusCode)
	}
	// Critical assertion: data must NOT have been deleted.
	if n := overviewTotal(t, ts); n != 1 {
		t.Errorf("post-invalid-purge request count = %d, want 1 (data must survive invalid JSON)", n)
	}
}

// TestPurgeObservation_BeforeTimestampDeletesOldOnly verifies that a {before}
// payload deletes only old data.
func TestPurgeObservation_BeforeTimestampDeletesOldOnly(t *testing.T) {
	ts, db := newPurgeTestServer(t)

	old := time.Now().Add(-40 * 24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	seedRequestLog(t, db, "req-old", old)
	seedRequestLog(t, db, "req-recent", recent)

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	body, _ := json.Marshal(map[string]string{"before": cutoff.UTC().Format(time.RFC3339)})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/observation/purge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("purge before: status %d: %s", resp.StatusCode, b)
	}
	var result struct {
		Deleted int `json:"deleted"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result.Deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only old row)", result.Deleted)
	}
	if n := overviewTotal(t, ts); n != 1 {
		t.Errorf("post-purge request count = %d, want 1 (recent must survive)", n)
	}
}

// --- helpers ---

// newPurgeTestServer creates a test server backed by a real SQLite DB whose
// handle is returned for direct seeding. The config is minimal (no routes
// needed — we test the purge/store layer, not the data plane).
func newPurgeTestServer(t *testing.T) (*httptest.Server, *desktopstore.DB) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "desktop.yaml")
	dbPath := filepath.Join(dir, "test.db")

	// Minimal config: one provider so the watcher builds successfully.
	minimal := `gateway:
  addr: "127.0.0.1:9999"
  session_headers: [X-Voxeltoad-Session]
providers:
  - name: p1
    type: openai
    endpoints:
      - id: openai
        adapter: openai
        base_url: http://127.0.0.1:1
    api_key_ref: "plain://k1"
    timeouts: {connect: 1s, first_byte: 1s, overall: 1s}
    weight: 1
models:
  - alias: m1
    upstreams:
      - provider: p1
        upstream_model: m1up
routes:
  - model_alias: m1
    strategy: priority
    providers: [{name: p1, weight: 1}]
settings:
  trace: {capture_payload_enabled: true, max_body_kb: 64, retention_days: 7}
`
	if err := os.WriteFile(cfgPath, []byte(minimal), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	db, err := desktopstore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dynFn, err := desktopcfg.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	watcher := app.NewDispatcherWatcher(dynFn, proxy.DispatcherConfig{})
	if err := watcher.Build(); err != nil {
		t.Fatalf("initial dispatcher build: %v", err)
	}

	srv := New(db, cfgPath, watcher, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, db
}

func seedRequestLog(t *testing.T, db *desktopstore.DB, requestID string, ts time.Time) {
	t.Helper()
	row := desktopstore.RequestLogRow{
		Tenant: "default", Provider: "p", ModelRequested: "m",
		RequestID: requestID, CreatedAt: ts,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed request_log: %v", err)
	}
}

func overviewTotal(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	code, b := getBody(t, ts, "/api/v1/overview")
	if code != 200 {
		t.Fatalf("overview: %d %s", code, b)
	}
	var result struct {
		Scalars struct {
			TotalRequests int `json:"total_requests"`
		} `json:"scalars"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	return result.Scalars.TotalRequests
}
