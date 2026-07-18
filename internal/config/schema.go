package config

import "time"

// This file defines the dynamic configuration schema: the typed shape of the
// providers, models, routes, and plugin configs the admin plane owns and the
// data plane consumes. It is the single source of truth for config structure;
// feature steps populate these types but should not reshape them (see the plan
// and design/architecture.md).

// Provider is an upstream LLM provider endpoint.
type Provider struct {
	// Name is the unique provider identifier / instance name (e.g.
	// "openai-prod", "tencent-hunyuan").
	Name string `json:"name"`
	// Type is the provider brand (e.g. "openai", "tencent", "zhipu",
	// "anthropic"). It is descriptive/observability-facing and does NOT select
	// the adapter — Adapter does. See ADR-0001.
	Type string `json:"type"`
	// Adapter selects the protocol adapter from the registry ("openai" or
	// "claude"). Multiple brands share one adapter: tencent/zhipu/compatible
	// providers all use adapter "openai". See ADR-0001.
	Adapter string `json:"adapter"`
	// BaseURL is the upstream API base URL.
	BaseURL string `json:"base_url"`
	// APIKeyRef references the upstream credential. Supported forms (ADR-0003/0030):
	//   "env://VAR_NAME" — resolved from the named environment variable;
	//   "db://provider/<name>" — resolved from the encrypted provider_credentials table;
	//   "plain://literal" or a bare literal — used as-is (dev/test only).
	// A SecretResolver extension point allows future schemes (e.g. vault://).
	// The resolved plaintext key MUST never be logged (see design/observability.md).
	APIKeyRef string `json:"api_key_ref"`
	// Timeouts configures the proxy's layered timeouts for this provider.
	Timeouts ProviderTimeouts `json:"timeouts"`
	// Weight is the relative load-balancing weight (used by routing).
	Weight int `json:"weight"`
}

// ProviderTimeouts holds the three distinct timeouts that must be configured
// separately (see design/e2e.md Pitfalls: conflating them kills long streams).
type ProviderTimeouts struct {
	// Connect bounds establishing the TCP/TLS connection.
	Connect time.Duration `json:"connect"`
	// FirstByte bounds time to first response byte (TTFT ceiling).
	FirstByte time.Duration `json:"first_byte"`
	// Overall bounds the entire request; for streaming this should be generous.
	Overall time.Duration `json:"overall"`
}

// Model maps a client-facing model alias to one or more provider-specific
// upstreams. A single alias can be served by multiple providers (for failover /
// load balancing across providers), each with its own native model name and
// defaults — including across protocols (e.g. an OpenAI provider and a Claude
// provider under the same alias). See ADR-0002/0009/0011.
type Model struct {
	// Alias is the model name clients request (e.g. "default-chat", "gpt-4o").
	Alias string `json:"alias"`
	// Description is a human-readable summary shown in the model catalog and
	// management UI. Purely informational; not used by the data plane.
	Description string `json:"description,omitempty"`
	// ContextLength is the advertised maximum context window in tokens
	// (e.g. 128000). Informational only; the data plane does not enforce it.
	ContextLength int `json:"context_length,omitempty"`
	// Capabilities lists what the model supports (e.g. "vision",
	// "function_calling", "streaming"). Surfaced in the model catalog for
	// filtering; not enforced by the data plane.
	Capabilities []string `json:"capabilities,omitempty"`
	// Tags are free-form labels for categorization and search.
	Tags []string `json:"tags,omitempty"`
	// Upstreams maps each serving provider to its native model + defaults.
	Upstreams []ModelUpstream `json:"upstreams"`
}

// ModelUpstream is one provider's realization of a model alias.
type ModelUpstream struct {
	// Provider is the Provider.Name serving this alias.
	Provider string `json:"provider"`
	// UpstreamModel is the provider-native model name to send upstream.
	UpstreamModel string `json:"upstream_model"`
	// DefaultMaxTokens is injected by the normalization layer when a request
	// omits max_tokens (required by Claude; see ADR-0009). 0 = no default.
	DefaultMaxTokens int `json:"default_max_tokens,omitempty"`
	// Pricing drives billing (per-million-token rates) for this provider.
	Pricing Pricing `json:"pricing"`
}

// Pricing holds per-million-token rates used to compute cost from usage. Rates
// are int64 micro-units of the configured currency (ADR-0013) — integer
// arithmetic end-to-end, no float drift. The admin plane converts
// human-readable decimals to micro-units when persisting; the snapshot carries
// micro-units.
type Pricing struct {
	PromptPer1M     int64  `json:"prompt_per_1m"`
	CompletionPer1M int64  `json:"completion_per_1m"`
	Currency        string `json:"currency"`
	// CacheHitMultiplier is the billing multiplier applied to the cached portion
	// of prompt tokens (adapter.Usage.CachedPromptTokens), in int64 micro-units:
	// 1_000_000 = full price, 500_000 = half, 100_000 = 10%. A value of 0 means
	// "unconfigured" and is treated as full price (1_000_000) by Cost, so legacy
	// configurations without this field behave identically on upgrade. Operators
	// who want a cache discount must set a non-zero value explicitly. Range [0,
	// 1_000_000].
	CacheHitMultiplier int64 `json:"cache_hit_multiplier,omitempty"`
}

// RouteProvider is a candidate provider within a Route, with a per-route weight
// (used by the "weighted" strategy; see ADR-0011). A provider can thus carry
// different weights in different routes; Provider.Weight is the default.
type RouteProvider struct {
	Name   string `json:"name"`
	Weight int    `json:"weight,omitempty"`
}

// Route resolves a model alias to one or more providers with a selection
// strategy (for load balancing and failover).
type Route struct {
	// ModelAlias is the alias this route applies to.
	ModelAlias string `json:"model_alias"`
	// Providers is the ordered candidate provider list.
	Providers []RouteProvider `json:"providers"`
	// Strategy selects among providers ("priority", "weighted", "round_robin").
	Strategy string `json:"strategy"`
}

// PluginConfig configures one governance plugin instance.
type PluginConfig struct {
	// Name is the registered plugin name (e.g. "ratelimit", "audit").
	Name string `json:"name"`
	// Phase is "pre" or "post".
	Phase string `json:"phase"`
	// Params holds plugin-specific parameters, passed to the plugin factory.
	Params map[string]any `json:"params,omitempty"`
	// Enabled toggles the plugin without removing its config.
	Enabled bool `json:"enabled"`
	// Scope optionally limits the plugin to a tenant/model (empty = global).
	Scope string `json:"scope,omitempty"`
}

// GatewaySettings is the set of gateway-wide behavior parameters the admin
// plane owns and the data plane applies via the config snapshot (Dynamic.
// Settings). Unlike bootstrap config (loaded once at startup), these are
// hot-reloadable: a change is picked up by the next snapshot poll (≤ poll
// interval) and applied per-request without a gateway restart.
//
// New parameters only need a field + JSON tag here — no migration, since the
// backing store is a single JSONB document (gateway_settings.spec). Fields that
// cannot be hot-applied (e.g. channel sizes, OTel provider lifecycle) stay in
// bootstrap, not here.
type GatewaySettings struct {
	// Trace governs the LLM trace-payload capture ledger (ADR-0039).
	Trace TraceSettings `json:"trace"`
}

// TraceSettings holds the hot-reloadable trace-capture parameters. All three
// fields apply within one snapshot poll interval; no restart needed.
type TraceSettings struct {
	// CapturePayloadEnabled toggles full message + raw body capture into the
	// trace_payloads ledger. Hot-applied: the per-request accumulator reads it
	// every request, so flipping it takes effect on the next poll.
	CapturePayloadEnabled bool `json:"capture_payload_enabled"`
	// MaxBodyKB bounds the captured request/response bodies per request (0 =
	// uncapped). Hot-applied per request.
	MaxBodyKB int `json:"max_body_kb"`
	// RetentionDays is the trace-payload retention window (≤0 = default 7). Read
	// by the admin plane's daily partition-DROP TTL job (not the data plane).
	RetentionDays int `json:"retention_days"`
}
