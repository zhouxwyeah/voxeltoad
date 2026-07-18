package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeYAML is a small helper: dump content into a tempdir file and return
// its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desktop.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return path
}

const withGateway = `gateway:
  addr: "127.0.0.1:8787"
  session_headers:
    - X-Voxeltoad-Session
    - X-Custom-Session
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
  trace: {capture_payload_enabled: true, max_body_kb: 64, retention_days: 30}
`

// The CRUD write path must not clobber the bootstrap gateway: section —
// config.Dynamic has no gateway fields, so a naive round-trip drops them and
// the next restart silently falls back to :8080.
func TestSaveFile_PreservesGatewaySection(t *testing.T) {
	path := writeYAML(t, withGateway)

	dyn, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	// Simulate a CRUD edit: only the dynamic document is touched.
	dyn.Providers[0].Weight = 42
	if err := SaveFile(path, dyn, nil); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	gw, err := LoadGatewaySection(path)
	if err != nil {
		t.Fatalf("LoadGatewaySection: %v", err)
	}
	if gw.Addr != "127.0.0.1:8787" {
		t.Errorf("gateway.addr = %q, want 127.0.0.1:8787", gw.Addr)
	}
	if len(gw.SessionHeaders) != 2 || gw.SessionHeaders[1] != "X-Custom-Session" {
		t.Errorf("session_headers = %v, want [X-Voxeltoad-Session X-Custom-Session]", gw.SessionHeaders)
	}

	// The dynamic edit must have landed, too.
	re, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile after save: %v", err)
	}
	if re.Providers[0].Weight != 42 {
		t.Errorf("provider weight = %d, want 42", re.Providers[0].Weight)
	}

	// The bootstrap loader's view: gateway.addr must still parse from disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(raw), `addr: 127.0.0.1:8787`) {
		t.Errorf("saved YAML missing gateway addr:\n%s", raw)
	}
}

// A non-nil gateway override replaces the section (settings-editor path).
func TestSaveFile_GatewayOverride(t *testing.T) {
	path := writeYAML(t, withGateway)

	dyn, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	gw := &GatewaySection{Addr: "127.0.0.1:9999", SessionHeaders: []string{"X-New"}}
	if err := SaveFile(path, dyn, gw); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}

	got, err := LoadGatewaySection(path)
	if err != nil {
		t.Fatalf("LoadGatewaySection: %v", err)
	}
	if got.Addr != "127.0.0.1:9999" || len(got.SessionHeaders) != 1 || got.SessionHeaders[0] != "X-New" {
		t.Errorf("gateway = %+v, want addr 127.0.0.1:9999 headers [X-New]", got)
	}
}

// A file without a gateway section stays without one (no invented defaults).
func TestSaveFile_NoGatewayInFile(t *testing.T) {
	path := writeYAML(t, `providers: []
models: []
routes: []
`)
	dyn, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if err := SaveFile(path, dyn, nil); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if strings.Contains(string(raw), "gateway:") {
		t.Errorf("gateway section invented out of nowhere:\n%s", raw)
	}
}

// LoadGatewaySection on a missing file is a hard error (callers rely on it to
// refuse a save that would drop the listen address).
func TestLoadGatewaySection_MissingFile(t *testing.T) {
	if _, err := LoadGatewaySection(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("want error for missing file")
	}
}
