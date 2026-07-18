# ADR-0023: External moderation provider integration

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0003 (secret resolution), ADR-0021 (privacy constraints)

## Context

The gateway already provides deterministic content safety via built-in plugins:
- `sensitive` — keyword-match block (ADR-0021-aligned, Phase 2 D1)
- `pii` — regex-based PII detection and redaction (Phase 3 D2)

These rules are **offline-capable and low-latency**, but they cannot detect
**semantic-level** violations (hate speech, self-harm, sexual content, violence)
that require a model. The 2026-06-29 design plan §5.4 chooses "integrate external
moderation, do not self-built models" for this category.

## Decision

### ModerationProvider interface

Define a provider interface so the plugin can swap implementations without
changing the plugin code:

```go
// internal/plugin/moderation/provider.go

type ModerationProvider interface {
    // Check returns true when the content is flagged (should be blocked).
    // Categories is a list of moderation categories to check (empty = all).
    Check(ctx context.Context, content string, categories []string) (bool, error)
}
```

### Default implementation: OpenAI Moderation API

```go
// internal/plugin/moderation/openai.go

type OpenAIModerationProvider struct {
    Endpoint string // https://api.openai.com/v1/moderations
    APIKey   string
    client   *http.Client
}

// POST /v1/moderations → response.results[].flagged → boolean
```

- Endpoint default: `https://api.openai.com/v1/moderations`
- API key sourced from `env://VAR` reference (same pattern as ADR-0003)
- Request timeout: 2s (configurable)
- Categories: `hate`, `hate/threatening`, `self-harm`, `sexual`, `sexual/minors`,
  `violence`, `violence/graphic` (OpenAI default set)

### Plugin config

```go
type Config struct {
    Enabled     bool     `json:"enabled"`
    Provider    string   `json:"provider"`              // "openai"
    Endpoint    string   `json:"endpoint,omitempty"`    // override default
    APIKeyRef   string   `json:"api_key_ref"`           // env://VAR
    Action      string   `json:"action"`                // "block" (default) | "flag"
    FailMode    string   `json:"fail_mode,omitempty"`   // "open" (default) | "closed"
    TimeoutMs   int      `json:"timeout_ms,omitempty"`  // default 2000
    Categories  []string `json:"categories,omitempty"`   // empty = all
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `action` | `"block"` | `"block"` → `Stop=true`, `BlockedBy="moderation"`; `"flag"` → `BlockedBy="moderation"` but `Stop=false` — request continues, event recorded in telemetry (`llm.plugin.blocked_by`) |
| `fail_mode` | `"open"` | `"open"` → moderation unavailable = allow request; `"closed"` → moderation unavailable = block request |

### Timeout and degradation

The moderation call runs on the **request hot path** (Pre-phase). To avoid
adding uncontrolled latency:

- Timeout default: **2 seconds** (configurable via `timeout_ms`)
- If the moderation API times out or errors:
  - `fail_mode = "open"` → log warning, allow request through
  - `fail_mode = "closed"` → block request (429 Too Many Requests with
    `Retry-After: 30`)

This is stricter than the billing async pattern (ADR-0016: fail-open,
non-blocking) because moderation is a **safety** concern, not an accounting
concern.

### Plugin registration

```go
func init() {
    plugin.Register("moderation", func(cfg any) (plugin.Plugin, error) {
        c := cfg.(Config)
        provider := buildProvider(c)
        return NewPlugin(c, provider), nil
    })
}
```

Provider construction reads `api_key_ref` via `config.ResolveSecret(ref)` (same
pattern as ADR-0003).

### Content scoping

**Message concatenation**: The plugin checks the first `N` characters of the
concatenated user messages (default 4096 chars). Rationale:

- Single API call covers all user messages without per-message round-trips.
- 4096 chars covers typical user prompts while staying well within OpenAI
  moderation's request body limit (~32 KB text). Truncation is by character
  count, not token count, to avoid an unnecessary tokenizer dependency.
- Truncation is configurable via `max_content_chars` (not in v1 Config schema,
  deferred to v2).

**Only user messages are checked** — system and assistant messages are excluded
by convention.

### Privacy constraint

As with all content-safety plugins, the moderation request body is **never
logged** to the request log or general observability (ADR-0021 §2). The only
observability surface is:

- `llm.plugin.blocked_by = "moderation"` on intercept
- Moderation API error count in OTel metrics (new counter:
  `llm_moderation_errors_total`)

## Consequences

### Positive

- Plugs into the existing plugin framework without new infrastructure.
- Default OpenAI moderation provider is widely available and free (no per-request
  cost).
- Configurable `fail_mode` lets operators choose safety posture per deployment.
- Provider interface is swappable — cloud vendors (Azure Content Safety, Alibaba
  Cloud Content Moderation) can be added as additional implementations.

### Negative

- Adds external network dependency to the request hot path. Mitigated by timeout
  + `fail_mode`.
- `action: "flag"` reuses `BlockedBy` without `Stop` as a workaround
  (see Decision); the `BlockedBy` semantic is conflated. A distinct
  `Context.Warning` field (plugin framework v2) would distinguish "blocked"
  from "flagged" clearly.
- OpenAI moderation API has rate limits (free tier: 100/min). Enterprise
  deployments may need a dedicated moderation endpoint.

### Limitations deferred to Phase 4c+

- **Response content moderation**: Post-phase moderation of assistant responses
  is not in v1 scope. Full lifecycle moderation adds 2 external API calls per
  request (pre + post), doubling the latency risk.
- **`Context.Warning` field**: tracked as a plugin framework v2 enhancement for
  distinguishing "blocked" (critical) from "flagged" (observable only)
  semantics. Workaround: set `BlockedBy` without `Stop` (see Decision).

## Related

- ADR-0003: API Key secret resolution (`env://VAR` pattern)
- ADR-0021: Request logs privacy constraint (never log prompt/moderation content)
- design/observability.md: Semantic telemetry fields
- design/architecture.md §5.4: Content safety — self-built rules + external
  moderation
