package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voxeltoad/cmd/desktop/config"
)

// TestConfigTemplateParses guards the embedded YAML template: any edit that
// breaks yaml.Unmarshal or the json round-trip in config.Load fails here.
func TestConfigTemplateParses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "desktop.yaml")
	if err := EnsureTemplate(p); err != nil {
		t.Fatalf("EnsureTemplate: %v", err)
	}
	dynFn, err := config.Load(p)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	dyn := dynFn()
	// Anti-regression: the written config must not contain hardcoded upstream
	// keys. Real keys come from GATEWAY_SEED_*_KEY env vars, never the source.
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if strings.Contains(string(data), "sk-") {
		t.Fatal("template contains hardcoded upstream key")
	}
	if len(dyn.Providers) != 4 {
		t.Errorf("providers = %d, want 4", len(dyn.Providers))
	}
	if len(dyn.Models) != 5 {
		t.Errorf("models = %d, want 5", len(dyn.Models))
	}
	if len(dyn.Routes) != 4 {
		t.Errorf("routes = %d, want 4", len(dyn.Routes))
	}
	// Spot-check a timeout parsed correctly (time.Duration via yaml.v3 "2s").
	if len(dyn.Providers) > 0 {
		p := dyn.Providers[0]
		if p.Timeouts.Connect == 0 {
			t.Errorf("provider %q timeouts.connect = 0, want non-zero", p.Name)
		}
	}
	// Spot-check pricing round-trip.
	if len(dyn.Models) > 0 {
		m := dyn.Models[0]
		if len(m.Upstreams) == 0 || m.Upstreams[0].Pricing.PromptPer1M == 0 {
			t.Errorf("model %q upstream[0] pricing empty: %+v", m.Alias, m.Upstreams)
		}
	}
}
