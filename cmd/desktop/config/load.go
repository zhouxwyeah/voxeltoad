// Package config loads the desktop gateway's dynamic configuration from a
// local YAML file (replacing the enterprise admin-snapshot poller,
// design/desktop.md §7). The data plane consumes config.Dynamic; we define
// yaml-tagged mirror structs and round-trip through JSON so the exact
// config.Dynamic shape (and its json tags) is reused without a hand-written
// field copy.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"voxeltoad/internal/config"
)

// --- yaml-tagged mirrors of config.Dynamic's sub-types ---

// The mirror structs keep yaml tags for parsing the on-disk file and add json
// tags identical to internal/config.Dynamic's field tags, so the yaml -> JSON ->
// config.Dynamic round-trip lands each field in the right place. Without the
// json tags, Go would marshal Go-style camelCase names (e.g. "APIKeyRef") that
// the enterprise structs do not recognize, leaving every field empty.
type yamlProvider struct {
	Name      string               `yaml:"name" json:"name"`
	Type      string               `yaml:"type" json:"type"`
	Adapter   string               `yaml:"adapter" json:"adapter"`
	BaseURL   string               `yaml:"base_url" json:"base_url"`
	APIKeyRef string               `yaml:"api_key_ref" json:"api_key_ref"`
	Weight    int                  `yaml:"weight" json:"weight"`
	Timeouts  yamlProviderTimeouts `yaml:"timeouts" json:"timeouts"`
}

type yamlProviderTimeouts struct {
	Connect   time.Duration `yaml:"connect" json:"connect"`
	FirstByte time.Duration `yaml:"first_byte" json:"first_byte"`
	Overall   time.Duration `yaml:"overall" json:"overall"`
}

type yamlModel struct {
	Alias         string              `yaml:"alias" json:"alias"`
	Description   string              `yaml:"description,omitempty" json:"description,omitempty"`
	ContextLength int                 `yaml:"context_length,omitempty" json:"context_length,omitempty"`
	Capabilities  []string            `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Tags          []string            `yaml:"tags,omitempty" json:"tags,omitempty"`
	Upstreams     []yamlModelUpstream `yaml:"upstreams" json:"upstreams"`
}

type yamlModelUpstream struct {
	Provider         string      `yaml:"provider" json:"provider"`
	UpstreamModel    string      `yaml:"upstream_model" json:"upstream_model"`
	DefaultMaxTokens int         `yaml:"default_max_tokens,omitempty" json:"default_max_tokens,omitempty"`
	Pricing          yamlPricing `yaml:"pricing" json:"pricing"`
}

type yamlPricing struct {
	PromptPer1M        int64  `yaml:"prompt_per_1m" json:"prompt_per_1m"`
	CompletionPer1M    int64  `yaml:"completion_per_1m" json:"completion_per_1m"`
	Currency           string `yaml:"currency" json:"currency"`
	CacheHitMultiplier int64  `yaml:"cache_hit_multiplier,omitempty" json:"cache_hit_multiplier,omitempty"`
}

type yamlRoute struct {
	ModelAlias string              `yaml:"model_alias" json:"model_alias"`
	Providers  []yamlRouteProvider `yaml:"providers" json:"providers"`
	Strategy   string              `yaml:"strategy" json:"strategy"`
}

type yamlRouteProvider struct {
	Name   string `yaml:"name" json:"name"`
	Weight int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

type yamlPluginConfig struct {
	Name    string         `yaml:"name" json:"name"`
	Phase   string         `yaml:"phase" json:"phase"`
	Params  map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Scope   string         `yaml:"scope,omitempty" json:"scope,omitempty"`
}

type yamlGatewaySettings struct {
	Trace yamlTraceSettings `yaml:"trace" json:"trace"`
}

type yamlTraceSettings struct {
	CapturePayloadEnabled bool `yaml:"capture_payload_enabled" json:"capture_payload_enabled"`
	MaxBodyKB             int  `yaml:"max_body_kb" json:"max_body_kb"`
	RetentionDays         int  `yaml:"retention_days" json:"retention_days"`
}

// GatewaySection is the bootstrap `gateway:` section of the desktop YAML
// (listen addr + session headers), consumed once at startup by the enterprise
// bootstrap loader (internal/config.Load). It is NOT part of config.Dynamic,
// so the CRUD write path must carry it alongside the dynamic document —
// otherwise every UI save would silently drop it and the next restart would
// fall back to the enterprise default ":12800". Only the fields the desktop
// template and the settings editor use are modeled; enterprise-only bootstrap
// knobs (rate_limit, max_tokens_ceiling, ...) are not supported in desktop
// YAML.
type GatewaySection struct {
	Addr           string   `yaml:"addr" json:"addr"`
	SessionHeaders []string `yaml:"session_headers,omitempty" json:"session_headers,omitempty"`
}

// yamlGateway mirrors GatewaySection for the on-disk YAML shape. It rides
// inside yamlDynamic with json:"-" so the yaml -> JSON -> config.Dynamic
// round-trip (which has no gateway concept) ignores it, while SaveFile can
// graft it back onto the emitted document.
type yamlGateway struct {
	Addr           string   `yaml:"addr" json:"-"`
	SessionHeaders []string `yaml:"session_headers,omitempty" json:"-"`
}

type yamlDynamic struct {
	// Gateway is declared first so the emitted YAML keeps the template's
	// section order (gateway on top). json:"-" excludes it from the
	// config.Dynamic round-trip; LoadFile/SaveFile handle it explicitly.
	Gateway   *yamlGateway         `yaml:"gateway,omitempty" json:"-"`
	Version   string               `yaml:"version" json:"version"`
	Providers []yamlProvider       `yaml:"providers" json:"providers,omitempty"`
	Models    []yamlModel          `yaml:"models" json:"models,omitempty"`
	Routes    []yamlRoute          `yaml:"routes" json:"routes,omitempty"`
	Plugins   []yamlPluginConfig   `yaml:"plugins" json:"plugins,omitempty"`
	Settings  *yamlGatewaySettings `yaml:"settings,omitempty" json:"settings,omitempty"`
}

// LoadFromFile reads + parses the YAML at path into a config.Dynamic via the
// yaml-mirror -> JSON -> config.Dynamic round-trip (reuses enterprise json tags).
// On parse error returns a zero-value Dynamic + the error so callers can decide
// whether to fail (startup) or keep last-good (rebuild). Exported so the config
// CRUD handlers in internal/desktopapi can read the on-disk file directly.
func LoadFromFile(path string) (*config.Dynamic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var yd yamlDynamic
	if err := yaml.Unmarshal(data, &yd); err != nil {
		return nil, err
	}
	jb, err := json.Marshal(yd)
	if err != nil {
		return nil, err
	}
	var dyn config.Dynamic
	if err := json.Unmarshal(jb, &dyn); err != nil {
		return nil, err
	}
	if dyn.Version == "" {
		dyn.Version = "local"
	}
	return &dyn, nil
}

// Load reads the dynamic config YAML at path once to validate syntax (fail-fast
// at startup), then returns a closure that RE-READS the file on every call.
// The closure is fed to app.NewDispatcherWatcher, whose rebuild path invokes
// it once per rebuild — so the re-read cost is paid only when a rebuild is
// triggered (startup, hot-reload after a config-API write, or the periodic
// Watch poll). A read error at call time returns nil; the watcher treats nil
// as "empty config" and keeps the last-good dispatcher (see watcher.go:63).
func Load(path string) (func() *config.Dynamic, error) {
	if _, err := LoadFromFile(path); err != nil {
		return nil, err
	}
	return func() *config.Dynamic {
		dyn, _ := LoadFromFile(path)
		return dyn
	}, nil
}

// LoadGatewaySection reads only the bootstrap `gateway:` section from the YAML
// at path. A missing section yields the zero value (not an error), so callers
// can distinguish "no gateway configured" from a corrupt file.
func LoadGatewaySection(path string) (GatewaySection, error) {
	var gw GatewaySection
	data, err := os.ReadFile(path)
	if err != nil {
		return gw, err
	}
	var yd yamlDynamic
	if err := yaml.Unmarshal(data, &yd); err != nil {
		return gw, err
	}
	if yd.Gateway != nil {
		gw.Addr = yd.Gateway.Addr
		gw.SessionHeaders = yd.Gateway.SessionHeaders
	}
	return gw, nil
}

// SaveFile serializes dyn back to the YAML file at path, preserving the
// bootstrap `gateway:` section that config.Dynamic does not model. When
// gateway is nil the section is re-read from the existing file at save time
// (the CRUD path: providers/models/routes edits must not clobber it); a
// non-nil gateway replaces the section (the settings-editor path). The write
// is atomic: marshal -> temp file in the same dir -> rename, so a concurrent
// reader (e.g. a rebuild in flight) never sees a half-written file. Callers
// should bump dyn.Version before saving so DispatcherWatcher.rebuild sees a
// change.
func SaveFile(path string, dyn *config.Dynamic, gateway *GatewaySection) error {
	// config.Dynamic -> JSON -> yaml-mirror -> YAML. The JSON hop reuses the
	// enterprise struct's json tags; the mirror's yaml tags land each field
	// under the on-disk key the reader expects. The gateway section rides
	// outside the JSON hop (json:"-") and is grafted on below.
	jb, err := json.Marshal(dyn)
	if err != nil {
		return err
	}
	var yd yamlDynamic
	if err := json.Unmarshal(jb, &yd); err != nil {
		return err
	}

	gw := gateway
	if gw == nil {
		existing, err := LoadGatewaySection(path)
		if err != nil {
			// The caller just read this file successfully; a read failure here
			// means the file vanished or was corrupted mid-write. Refusing to
			// save is safer than silently dropping the listen address.
			return fmt.Errorf("read existing gateway section: %w", err)
		}
		gw = &existing
	}
	if gw.Addr != "" || len(gw.SessionHeaders) > 0 {
		yd.Gateway = &yamlGateway{Addr: gw.Addr, SessionHeaders: gw.SessionHeaders}
	}

	out, err := yaml.Marshal(yd)
	if err != nil {
		return err
	}
	dir := "."
	if d := filepath.Dir(path); d != "" {
		dir = d
	}
	tmp, err := os.CreateTemp(dir, ".desktop-config-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
