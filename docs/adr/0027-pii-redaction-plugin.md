# ADR-0027: PII detection and redaction plugin

- Status: Accepted
- Date: 2026-07-04
- Builds on: ADR-0021 (privacy constraints — never log prompt content)

## Context

LLM request messages may contain personally identifiable information (PII):
email addresses, phone numbers, ID card numbers, and credit card numbers.
Operators need the ability to either block requests containing PII or redact
the values before forwarding them upstream.

The plugin framework (`internal/plugin/plugin.go`) already supports Pre-phase
plugins with `Stop`/`BlockedBy`, and the sensitive word plugin (D1) established
the pattern for content scanning as a Pre-phase check.

## Decision

### Plugin: `internal/plugin/pii/`

- Pre-phase only
- Configurable per-deployment via `PluginConfig`
- Two modes: `block` (reject) and `redact` (replace + continue)

### Detection patterns

Four regular expressions covering high-precision PII:

| Detector | Pattern | Replace token |
|----------|---------|---------------|
| `email` | `user@domain.tld` format | `[EMAIL]` |
| `phone` | 1[3-9]x-xxxx-xxxx (11 digits, optional dashes) | `[PHONE]` |
| `id_card` | 18-digit CN ID card (area + birthday + seq + checksum) | `[ID_CARD]` |
| `credit_card` | 13-19 digits with optional dashes/spaces | `[CREDIT_CARD]` |

The `credit_card` detector is intentionally broad (any 13-19 digit sequence) and
may produce false positives on numeric IDs. Operators can disable it via the
`patterns` config field.

### Block vs Redact

```go
type Config struct {
    Enabled  bool     `json:"enabled"`
    Mode     string   `json:"mode"`     // "block" | "redact"
    Patterns []string `json:"patterns"` // whitelist; empty = all
}
```

- `block`: `Stop=true`, `BlockedBy="pii"` → request rejected, telemetry recorded
- `redact`: PII replaced with `[EMAIL]`/`[PHONE]` etc. in `c.Request.Messages`,
  `BlockedBy="pii"` set as metadata (but `Stop=false`) → request forwarded
  with redacted content

### Privacy constraint

The plugin **never logs message content** — only `BlockedBy="pii"` and the hit
classification are recorded in telemetry. The request log (`request_logs`) does
not store prompt or completion bodies (ADR-0021).

### Registration

Standard `plugin.Factory` pattern via `init()`:

```go
func init() {
    plugin.Register("pii", func(cfg any) (plugin.Plugin, error) {
        c, ok := cfg.(Config)
        // ...
        return NewPlugin(c), nil
    })
}
```

## Consequences

### Positive

- Operators can choose between hard-block and transparent-redact per deployment.
- Uses existing plugin infrastructure — no new framework code.
- Regex patterns are deterministic and offline-capable (no external API call).

### Negative

- Regex-based detection is not semantic — a credit card number embedded in a
  code example or token count is also redacted (false positive).
- `redact` mode mutates `c.Request.Messages` in-place. Subsequent plugins see
  the redacted content (by design; prevents downstream leakage).
- No Unicode-normalization step — CJK full-width digits are not detected.
  This is acceptable for Phase 3 scope.

## Related

- ADR-0021: request logs privacy constraint
- Plan D1/D2: sensitive word + PII plugins
- design/observability.md: `llm.plugin.blocked_by` semantic field
