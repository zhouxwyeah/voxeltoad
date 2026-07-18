package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Bootstrap holds static settings needed to start a process. It is loaded once
// at startup and does not change at runtime.
type Bootstrap struct {
	Gateway  GatewayConfig  `yaml:"gateway"`
	Admin    AdminConfig    `yaml:"admin"`
	Snapshot SnapshotConfig `yaml:"snapshot"`
	DB       DBConfig       `yaml:"db"`
	OTel     OTelConfig     `yaml:"otel"`
	Trace    TraceConfig    `yaml:"trace"`
}

// GatewayConfig is the data-plane listen configuration.
type GatewayConfig struct {
	Addr string `yaml:"addr"`
	// SessionHeaders lists the HTTP header names (priority order) consulted to
	// extract the session key for session_affinity routing (ADR-0018). Empty
	// uses the default (X-Voxeltoad-Session). Add an agent framework's header here to
	// support it — no per-agent code.
	SessionHeaders []string `yaml:"session_headers,omitempty"`
	// MaxTokensCeiling bounds the billing quota reservation's completion-token
	// estimate when a request omits max_tokens and config sets no
	// DefaultMaxTokens (ADR-0013). <= 0 uses the billing default. Prevents a
	// zero estimate silently bypassing the quota pre-debit.
	MaxTokensCeiling int64 `yaml:"max_tokens_ceiling,omitempty"`
	// RateLimit configures per-identity RPM/TPM limits (ADR-0008). All-zero
	// disables rate limiting (the plugin is not installed). Static/bootstrap
	// config in P0; limits are per-data-plane-instance (in-memory).
	RateLimit RateLimitConfig `yaml:"rate_limit,omitempty"`
	// AllowInsecureDev permits an empty internal_token_ref on the snapshot
	// channel (ADR-0007). Production MUST leave this false. Set via
	// GATEWAY_ALLOW_INSECURE_DEV=1 for local dev / devstack only.
	AllowInsecureDev bool `yaml:"allow_insecure_dev,omitempty"`
	// ProviderCredentialKEKRef references a base64-encoded 32-byte key used to
	// encrypt/decrypt provider credentials stored in the provider_credentials
	// table (ADR-0031). Example: "env://GATEWAY_PROVIDER_CREDENTIAL_KEK". When
	// empty, encrypted db://provider/<name> references cannot be resolved.
	ProviderCredentialKEKRef string `yaml:"provider_credential_kek_ref,omitempty"`
}

// RateLimitConfig holds per-identity request/token rate limits, applied at the
// tenant, group, and key levels (ADR-0005/0008). A zero on any dimension means
// "no limit" on it.
type RateLimitConfig struct {
	TenantRPM int `yaml:"tenant_rpm,omitempty"`
	TenantTPM int `yaml:"tenant_tpm,omitempty"`
	GroupRPM  int `yaml:"group_rpm,omitempty"`
	GroupTPM  int `yaml:"group_tpm,omitempty"`
	KeyRPM    int `yaml:"key_rpm,omitempty"`
	KeyTPM    int `yaml:"key_tpm,omitempty"`
	// Window is the sliding window for all dimensions (default 1m if zero).
	Window time.Duration `yaml:"window,omitempty"`
}

// Enabled reports whether any rate-limit dimension is configured.
func (r RateLimitConfig) Enabled() bool {
	return r.TenantRPM > 0 || r.TenantTPM > 0 || r.GroupRPM > 0 ||
		r.GroupTPM > 0 || r.KeyRPM > 0 || r.KeyTPM > 0
}

// AdminConfig is the management-plane listen configuration.
type AdminConfig struct {
	Addr string `yaml:"addr"`
	// AllowedOrigins enables CORS for the listed browser origins on the admin
	// API (ADR-0019: front-end/back-end separation). Empty = CORS disabled
	// (same-origin only). Set to the Control Panel UI origin(s).
	AllowedOrigins []string `yaml:"allowed_origins,omitempty"`
}

// SnapshotConfig tells the data plane where to pull dynamic config from and how
// often to poll the admin plane's config-snapshot endpoint.
type SnapshotConfig struct {
	// AdminURL is the base URL of the admin plane, e.g. "http://localhost:8090".
	AdminURL string `yaml:"admin_url"`
	// PollInterval is how often the data plane polls for config changes.
	PollInterval time.Duration `yaml:"poll_interval"`
	// InternalTokenRef references the shared secret authenticating the
	// data↔management snapshot channel (ADR-0007), resolvable via ResolveSecret
	// (e.g. "env://GATEWAY_INTERNAL_TOKEN"). Empty leaves the channel open
	// (dev/test only). Both planes must configure the same resolved value.
	InternalTokenRef string `yaml:"internal_token_ref"`
}

// DBConfig configures the PostgreSQL connection (admin plane).
type DBConfig struct {
	DSN string `yaml:"dsn"`
}

// OTelConfig configures OpenTelemetry export.
type OTelConfig struct {
	Endpoint    string `yaml:"endpoint"`
	ServiceName string `yaml:"service_name"`
	Enabled     bool   `yaml:"enabled"`
}

// TraceConfig holds the bootstrap-only trace-payload parameters (ADR-0039). The
// hot-reloadable knobs (capture on/off, max body KB, retention days) live in the
// gateway_settings document and are managed via the admin UI Settings page /
// PUT /api/v1/gateway-settings; they are NOT read from here. Only the
// non-hot-reloadable buffer size and a retention fallback default remain here.
type TraceConfig struct {
	CapturePayload TracePayloadConfig `yaml:"capture_payload"`
}

// TracePayloadConfig holds the bootstrap-only trace-payload cost parameters.
// enabled / max_body_kb / retention_days moved to the hot-reloadable
// GatewaySettings (config/schema.go); they are no longer read from bootstrap.
type TracePayloadConfig struct {
	// RetentionDays is the fallback retention window used by the admin TTL job
	// when the hot-reloadable GatewaySettings.Trace.RetentionDays is unset (<=0).
	// <= 0 means "use the default" (7 days).
	RetentionDays int `yaml:"retention_days,omitempty"`
	// Buffer is the async trace-payload recorder's bounded buffer size. <= 0 uses
	// the sensible default (mirrors the request-log recorder). NOT hot-reloadable
	// (channel size is fixed at recorder construction).
	Buffer int `yaml:"buffer,omitempty"`
}

// defaultTraceRetentionDays is the default short-retention window (days) for the
// trace_payloads ledger (ADR-0039). Capture is still off by default; this only
// applies once a deployment opts in.
const defaultTraceRetentionDays = 7

// Default returns a Bootstrap with sensible local-development defaults.
func Default() Bootstrap {
	return Bootstrap{
		Gateway:  GatewayConfig{Addr: ":8080"},
		Admin:    AdminConfig{Addr: ":8090"},
		Snapshot: SnapshotConfig{AdminURL: "http://localhost:8090", PollInterval: 5 * time.Second},
		DB:       DBConfig{DSN: "postgres://postgres:postgres@localhost:5432/voxeltoad?sslmode=disable"},
		OTel:     OTelConfig{ServiceName: "voxeltoad", Enabled: false},
		Trace:    TraceConfig{CapturePayload: TracePayloadConfig{RetentionDays: defaultTraceRetentionDays}},
	}
}

// Load reads bootstrap config from the YAML file at path, falling back to
// defaults for any unset fields. If path is empty, Default is returned.
func Load(path string) (Bootstrap, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	// GATEWAY_ALLOW_INSECURE_DEV=1 permits an empty internal_token_ref for local
	// dev / devstack (ADR-0007). Checked here so the env var is honored
	// regardless of how the YAML is built.
	if os.Getenv("GATEWAY_ALLOW_INSECURE_DEV") == "1" {
		cfg.Gateway.AllowInsecureDev = true
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate enforces production-safety invariants on the loaded bootstrap
// config. The snapshot channel's shared secret (internal_token_ref) is REQUIRED
// in production (ADR-0007): an empty token leaves the data↔management plane
// channel unauthenticated, so anyone reaching the admin port can pull the full
// config snapshot (provider base URLs, routing topology, etc.). Callers MAY
// opt out via GATEWAY_ALLOW_INSECURE_DEV=1 (→ gateway.allow_insecure_dev) for local
// dev / devstack only.
func (b Bootstrap) Validate() error {
	if b.Snapshot.InternalTokenRef == "" && !b.Gateway.AllowInsecureDev {
		return fmt.Errorf("config: snapshot.internal_token_ref must be set in production (see ADR-0007); set GATEWAY_ALLOW_INSECURE_DEV=1 for local dev")
	}
	return nil
}
