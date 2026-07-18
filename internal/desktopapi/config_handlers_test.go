package desktopapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	desktopcfg "voxeltoad/cmd/desktop/config"
	desktopseed "voxeltoad/cmd/desktop/seed"
	"voxeltoad/internal/app"
	"voxeltoad/internal/config"
	"voxeltoad/internal/desktopstore"
	"voxeltoad/internal/proxy"
)

// newConfigTestServer wires a Server with a real tempdir YAML + real
// DispatcherWatcher (so hot-reload is exercised end-to-end). Returns the live
// httptest server + the config path (for direct file inspection in assertions).
func newConfigTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "desktop.yaml")
	dbPath := filepath.Join(dir, "test.db")

	// Seed the default config template (one openai-shaped provider + one model).
	if err := desktopseed.EnsureTemplate(cfgPath); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	// But the default template points at env://OPENAI_API_KEY; swap to a mock.
	// Simplest: overwrite with a minimal in-process config we fully control.
	minimal := `gateway:
  addr: "127.0.0.1:9999"
  session_headers: [X-Voxeltoad-Session]
providers:
  - name: p1
    type: openai
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
		t.Fatalf("write minimal config: %v", err)
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

	srv := New(db, cfgPath, watcher)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cfgPath
}

func getBody(t *testing.T, ts *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func reqBody(t *testing.T, method, ts, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		jb, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(jb)
	}
	req, _ := http.NewRequest(method, ts+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// --- providers ---

func TestProviders_CRUD(t *testing.T) {
	ts, cfgPath := newConfigTestServer(t)

	// Initial: 1 provider (p1 from the minimal config).
	code, b := getBody(t, ts, "/api/v1/providers")
	if code != 200 {
		t.Fatalf("list providers: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"name":"p1"`) {
		t.Fatalf("expected p1 in list: %s", b)
	}

	// Create p2.
	code, b = reqBody(t, "POST", ts.URL, "/api/v1/providers", map[string]any{
		"name": "p2", "type": "openai", "adapter": "openai",
		"base_url": "http://127.0.0.1:2", "api_key_ref": "plain://k2", "weight": 1,
		// time.Duration over JSON = nanoseconds as integer (1s = 1e9).
		"timeouts": map[string]int64{"connect": 1_000_000_000, "first_byte": 1_000_000_000, "overall": 1_000_000_000},
	})
	if code != 201 {
		t.Fatalf("create p2: %d %s", code, b)
	}

	// GET the new one.
	code, b = getBody(t, ts, "/api/v1/providers/p2")
	if code != 200 || !strings.Contains(string(b), `"base_url":"http://127.0.0.1:2"`) {
		t.Errorf("get p2: %d %s", code, b)
	}

	// Update p2's weight.
	code, b = reqBody(t, "PUT", ts.URL, "/api/v1/providers/p2", map[string]any{
		"type": "openai", "adapter": "openai",
		"base_url": "http://127.0.0.1:2", "api_key_ref": "plain://k2", "weight": 99,
		"timeouts": map[string]int64{"connect": 1_000_000_000, "first_byte": 1_000_000_000, "overall": 1_000_000_000},
	})
	if code != 200 {
		t.Fatalf("update p2: %d %s", code, b)
	}

	// Verify the file on disk reflects the update.
	disk, err := desktopcfg.LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("reload disk config: %v", err)
	}
	var p2 *config.Provider
	for i := range disk.Providers {
		if disk.Providers[i].Name == "p2" {
			p2 = &disk.Providers[i]
		}
	}
	if p2 == nil || p2.Weight != 99 {
		t.Errorf("disk state: p2=%+v want weight=99", p2)
	}

	// Delete p2.
	code, b = reqBody(t, "DELETE", ts.URL, "/api/v1/providers/p2", nil)
	if code != 200 {
		t.Fatalf("delete p2: %d %s", code, b)
	}
	code, _ = getBody(t, ts, "/api/v1/providers/p2")
	if code != 404 {
		t.Errorf("get after delete: %d, want 404", code)
	}
}

func TestProviders_DeleteReferenceCheck(t *testing.T) {
	ts, _ := newConfigTestServer(t)
	// p1 is referenced by model m1; deleting it must 409.
	code, b := reqBody(t, "DELETE", ts.URL, "/api/v1/providers/p1", nil)
	if code != 409 {
		t.Errorf("delete referenced provider: %d %s, want 409", code, b)
	}
	if !strings.Contains(string(b), "referenced by model") {
		t.Errorf("409 body should explain reference: %s", b)
	}
}

func TestProviders_CreateDuplicate(t *testing.T) {
	ts, _ := newConfigTestServer(t)
	code, _ := reqBody(t, "POST", ts.URL, "/api/v1/providers", map[string]any{
		"name": "p1", "type": "x", "adapter": "openai", "base_url": "u", "api_key_ref": "plain://k", "weight": 1,
		"timeouts": map[string]int64{"connect": 1_000_000_000, "first_byte": 1_000_000_000, "overall": 1_000_000_000},
	})
	if code != 409 {
		t.Errorf("duplicate create: %d, want 409", code)
	}
}

// --- models ---

func TestModels_CRUD(t *testing.T) {
	ts, cfgPath := newConfigTestServer(t)

	// Create a second model m2 (provider p1 exists).
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/models", map[string]any{
		"alias": "m2",
		"upstreams": []map[string]any{
			{"provider": "p1", "upstream_model": "m2up", "pricing": map[string]any{"prompt_per_1m": 1, "completion_per_1m": 2, "currency": "usd"}},
		},
	})
	if code != 201 {
		t.Fatalf("create m2: %d %s", code, b)
	}

	// List should now have m1 and m2.
	code, b = getBody(t, ts, "/api/v1/models")
	if code != 200 || !strings.Contains(string(b), `"alias":"m2"`) {
		t.Errorf("list models: %d %s", code, b)
	}

	// Validate reference check: cannot delete m1 (route references it).
	code, _ = reqBody(t, "DELETE", ts.URL, "/api/v1/models/m1", nil)
	if code != 409 {
		t.Errorf("delete referenced model: %d, want 409", code)
	}

	// Delete m2 (no route references it).
	code, _ = reqBody(t, "DELETE", ts.URL, "/api/v1/models/m2", nil)
	if code != 200 {
		t.Errorf("delete m2: %d, want 200", code)
	}

	// Disk state reflects the deletion.
	disk, _ := desktopcfg.LoadFromFile(cfgPath)
	for _, m := range disk.Models {
		if m.Alias == "m2" {
			t.Errorf("m2 should be gone from disk: %+v", m)
		}
	}
}

func TestModels_ValidateUpstreamProvider(t *testing.T) {
	ts, _ := newConfigTestServer(t)
	// Create a model whose upstream references a non-existent provider.
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/models", map[string]any{
		"alias": "bad",
		"upstreams": []map[string]any{
			{"provider": "no-such-provider", "upstream_model": "x"},
		},
	})
	if code != 400 {
		t.Errorf("create model with unknown provider: %d %s, want 400", code, b)
	}
}

// --- routes ---

func TestRoutes_CRUD(t *testing.T) {
	ts, _ := newConfigTestServer(t)

	// Create a second route (model m1 + provider p1 both exist).
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/routes", map[string]any{
		"model_alias": "m1-backup",
		"strategy":    "priority",
		"providers":   []map[string]any{{"name": "p1", "weight": 1}},
	})
	if code != 201 {
		t.Fatalf("create route: %d %s", code, b)
	}

	// List should include both routes.
	code, b = getBody(t, ts, "/api/v1/routes")
	if code != 200 || !strings.Contains(string(b), `"model_alias":"m1-backup"`) {
		t.Errorf("list routes: %d %s", code, b)
	}

	// Delete m1-backup (no reference).
	code, _ = reqBody(t, "DELETE", ts.URL, "/api/v1/routes/m1-backup", nil)
	if code != 200 {
		t.Errorf("delete route: %d, want 200", code)
	}
}

func TestRoutes_ValidateProvider(t *testing.T) {
	ts, _ := newConfigTestServer(t)
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/routes", map[string]any{
		"model_alias": "bad",
		"strategy":    "priority",
		"providers":   []map[string]any{{"name": "ghost", "weight": 1}},
	})
	if code != 400 {
		t.Errorf("create route with unknown provider: %d %s, want 400", code, b)
	}
}

// --- reload ---

func TestConfig_Reload(t *testing.T) {
	ts, _ := newConfigTestServer(t)
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/config/reload", nil)
	if code != 200 {
		t.Fatalf("reload: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"status":"reloaded"`) {
		t.Errorf("reload body: %s", b)
	}
}

// --- hot-reload really takes effect ---

func TestConfig_HotReloadNewProviderUsable(t *testing.T) {
	// The proof that the CRUD API + hot-reload really works: create a provider
	// + model + route via the API, then verify the dispatcher can build (which
	// it must, since saveConfigAndReload called watcher.Build()). We assert
	// this indirectly: the reload endpoint returns 200 only if Build succeeded.
	ts, _ := newConfigTestServer(t)

	// Add a new provider + model + route via CRUD.
	code, b := reqBody(t, "POST", ts.URL, "/api/v1/providers", map[string]any{
		"name": "p-new", "type": "openai", "adapter": "openai",
		"base_url": "http://127.0.0.1:9", "api_key_ref": "plain://k", "weight": 1,
		"timeouts": map[string]int64{"connect": 1_000_000_000, "first_byte": 1_000_000_000, "overall": 1_000_000_000},
	})
	if code != 201 {
		t.Fatalf("create provider: %d %s", code, b)
	}
	code, b = reqBody(t, "POST", ts.URL, "/api/v1/models", map[string]any{
		"alias": "m-new",
		"upstreams": []map[string]any{
			{"provider": "p-new", "upstream_model": "newup", "pricing": map[string]any{"prompt_per_1m": 1, "completion_per_1m": 2, "currency": "usd"}},
		},
	})
	if code != 201 {
		t.Fatalf("create model: %d %s", code, b)
	}
	code, b = reqBody(t, "POST", ts.URL, "/api/v1/routes", map[string]any{
		"model_alias": "m-new", "strategy": "priority",
		"providers": []map[string]any{{"name": "p-new", "weight": 1}},
	})
	if code != 201 {
		t.Fatalf("create route: %d %s", code, b)
	}

	// Each successful 201 already proved watcher.Build() succeeded (the handler
	// returns 201 only after saveConfigAndReload's Build returned nil). So the
	// new config is live in the dispatcher. The explicit reload is belt+suspenders.
	code, _ = reqBody(t, "POST", ts.URL, "/api/v1/config/reload", nil)
	if code != 200 {
		t.Errorf("post-CRUD reload: %d, want 200", code)
	}
}
