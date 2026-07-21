# ADR-0048: Anthropic ingress global toggle

- Status: Accepted
- Date: 2026-07-22
- Builds on: [ADR-0045](0045-anthropic-ingress-protocol.md) (Anthropic ingress)

## Context

ADR-0045 made Anthropic ingress (`/v1/messages`) globally available — any valid API key can use it. Some operators want to disable it (e.g. a deployment that only serves OpenAI clients and doesn't want the extra endpoint exposed, or a gradual rollout where Anthropic support is enabled per-environment).

## Decision

Add a hot-reloadable global toggle in `GatewaySettings.Ingress.AnthropicDisabled` (JSONB-backed, no migration). When true, `messagesHandler` short-circuits `/v1/messages` requests with **404 + Anthropic error envelope** before reaching `serveChat` (no dispatcher, no telemetry, no billing).

### Field naming: `AnthropicDisabled` (not `AnthropicEnabled`)

The field is named "Disabled" so the **zero value is the permissive default**: a fresh deployment or a spec that omits the `ingress` block entirely (the seeded `{}` from migration 00021) results in `AnthropicDisabled = false` = enabled. No seed/migration/default-value-filling needed — the gateway's default behavior (all protocols on, ADR-0045) is preserved by Go's zero-value semantics.

### 404 vs 503

When disabled, the gateway returns **404 Not Found** (not 503 Service Unavailable):
- 404 is **terminal** — clients treat it as "endpoint does not exist" and stop retrying.
- 503 implies "temporarily unavailable, retry may succeed," which causes Anthropic clients (Claude Code) to enter retry loops, wasting tokens and user patience.

The error envelope uses the Anthropic shape (`{"type":"error","error":{"type":"not_found_error",...}}`) via the ingress codec, so the client can parse it.

### Hot-reload path

`settings PUT → repo.UpdateSettings → bump config_generation → data-plane poller (≤5s) → Dynamic.Settings atomic replace → next request reads new value`. Same path as ADR-0039's trace toggle. No gateway restart.

### Requests in flight when the toggle flips

The toggle is checked only at request entry (`messagesHandler`). A streaming response already in progress (past the entry check) runs to completion — the toggle does not interrupt mid-stream.

## Alternatives considered

- **Per-key `allowed_ingress_protocols`** (ADR-0033 pattern). Rejected for this scope: it's a finer-grained control that needs a migration + key form + auth middleware change. The global toggle covers the common case (enable/disable per deployment) with zero schema change.
- **503 Service Unavailable.** Rejected: triggers client retry loops.

## Consequences

- One new `IngressSettings` struct in `config/schema.go`, one check in `messagesHandler`.
- One new error-type mapping (`not_found_error` in the Anthropic codec).
- `not_found_error` is a route-level 404, not a business error — it bypasses telemetry/audit (the request is treated as if the endpoint doesn't exist).
