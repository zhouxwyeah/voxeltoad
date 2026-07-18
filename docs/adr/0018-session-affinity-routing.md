# ADR-0018: Session-affinity routing for provider-side prompt-cache locality

- Status: Accepted
- Date: 2026-07-01
- Builds on ADR-0011 (routing strategies & failover semantics), ADR-0002 (alias
  resolution in routing), ADR-0006 (client key auth, source of caller identity),
  ADR-0008/0011 (in-memory per-instance state trade-offs).

## Context

Hosted LLM providers offer **prompt caching** keyed by request **prefix**, per
provider: OpenAI automatic prefix caching (≥ ~1024 tokens), Anthropic explicit
`cache_control` breakpoints, Bedrock `cachePoint`, etc. A cache entry lives on
**one provider's side**; a request served by provider B cannot hit a prefix
cached at provider A — it is a different cache domain.

Our current routing strategies (`priority`, `weighted`, `round_robin`; ADR-0011)
are all **stateless per-request**. `weighted`/`round_robin` are in fact
**anti-cache**: they deliberately spread a caller's successive requests across
providers, so a multi-turn agent conversation (whose turns share a large stable
prefix — system prompt + history) lands on different providers and repeatedly
misses the provider-side cache. This wastes tokens and money.

**The requirement (primary driver: cost):** the same logical session should be
routed to the **same provider** as much as possible, so its shared prefix stays
in that provider's cache and hits on subsequent turns.

Scope note: this ADR addresses **provider-side (layer-A) affinity only** —
choosing among a route's candidate *providers*. Replica-level / self-hosted KV
prefix routing (SGLang/vLLM style intra-fleet cache-aware routing) is explicitly
out of scope (we proxy hosted providers; a "provider" here is an upstream, not a
replica we schedule across).

## Decision

### 1. New route strategy `session_affinity`

Add a fourth strategy alongside `priority`/`weighted`/`round_robin`. It is
selected per route (mutually exclusive with the others on that route). It is the
recommended strategy for multi-turn / agent traffic; `weighted`/`round_robin`
remain for stateless fan-out workloads.

`weighted` and `session_affinity` are conceptually opposite (spread vs. stick)
and must not be combined on one route.

### 2. Session key — a configurable priority chain, session-first

The session key is resolved by the **first** source that yields a non-empty
value, in this fixed priority order (session identity ranks **highest**, per the
requirement):

1. **A configured HTTP header** — default `X-Voxeltoad-Session`; operators may
   configure a list of candidate header names so different agent frameworks are
   supported by **configuration, not per-agent code**.
2. **Request-body identity fields** — `prompt_cache_key`, then `user` (the
   OpenAI-standard fields most agent clients already send).
3. **Stable-prefix fallback** — a hash of the request's stable prefix (system
   message + first user message). This groups a bare client's multi-turn
   conversation without any explicit session id, since those turns share the
   prefix.

Rationale: mainstream inference routers (vLLM router) use exactly this
header → body-field → content-hash precedence. Encoding "which header" as config
means onboarding a new agent is a config change, not a new code path. Only a
genuinely non-standard agent that buries its session id somewhere exotic would
warrant a bespoke extractor (strategy-style), added without touching the main
path.

### 3. Provider selection — Rendezvous (HRW) hashing, producing a full ordering

For a session key `k` and the route's candidate providers, score each provider
`p` as `hash(k + "|" + p.Name)` and **order all candidates by descending score**.
This yields a **deterministic ordering** (not just a single pick): the same `k`
always produces the same order.

Rendezvous / Highest-Random-Weight hashing is chosen over ring-based consistent
hashing because it needs no vnode/ring-size tuning, has no arc-bias, and
redistributes only ~1/n of keys when a provider is added/removed — and vLLM
router's own measurements show HRW is materially more balanced than a ring as
session count grows.

### 4. Orthogonal to failover and circuit-breaking — no changes to either

`session_affinity` only changes **how the candidate list is ordered**; the rest
of ADR-0011 is untouched:

- The HRW ordering is fed through the existing health filter and failover loop.
  The affinity provider sits first **when healthy**; when it is
  breaker-unhealthy it is dropped and the **HRW second choice (equally
  deterministic for that key) takes over** — so even the *fallback* provider is
  consistent for a given session, preserving cache locality on the backup too.
- Failover triggers (retryable-only), the streaming first-byte boundary, and
  billing-on-actual-provider (ADR-0011) all apply unchanged.

### 5. Best-effort, not guaranteed; stateless and multi-instance-safe

Affinity is **best-effort**: availability and correctness win over hit-rate. If
the affinity provider is unhealthy, we serve from the next deterministic choice
rather than fail. Cache hits are never guaranteed anyway (provider TTLs, prefix
thresholds).

Unlike `round_robin`'s per-instance cursor, HRW is a **pure function of
(session key, provider set)** — it holds no state, so it is **automatically
consistent across data-plane replicas** with no shared store. This sidesteps the
per-instance inconsistency caveat that ADR-0008/0011 carry, and is a bonus of
the HRW choice.

#### Fallback (prefix hash) aggregation — known limits

The priority chain's last resort (level 6) hashes the system message (truncated
to its first 512 bytes) plus the first user message. This is **best-effort
aggregation**, not a guarantee, and has two known limits operators should be
aware of when relying on session-scoped cost/timeline views:

- **Coding-agent system-prompt drift.** Agents like Claude Code prepend a stable
  identity block followed by a dynamic tail (env block with wall-clock time, git
  branch, cwd, token budget). The 512-byte cut shields the hash from that tail
  in the common case, but if the *leading* identity block itself changes (prompt
  template revision, role switch), the same logical session splits across two
  prefix-hash values.
- **Mixed agent versions.** A new agent release that starts emitting a
  `x-<vendor>-session-id` header (e.g. Claude Code v2.1.86 introduced
  `X-Claude-Code-Session-Id`) means the same logical session has pre-upgrade
  requests on the prefix-hash path and post-upgrade requests on the header path,
  producing two distinct `session_id` values and therefore two rows in
  `ListSessions`. There is no reliable way to merge these after the fact; the
  split closes as the fleet upgrades.

**Guidance:** when session-aggregation completeness matters (cost attribution,
per-conversation auditing), have clients send an explicit session header
(`X-Voxeltoad-Session` or the vendor convention) rather than relying on the fallback.

### 6. Preserve prefix stability (enabling condition)

Provider-side caching is prefix-exact, so the gateway must not reorder or mutate
the cacheable prefix. The normalization layer (ADR-0009) already produces a
deterministic request; this ADR adds the constraint that any future
normalization stays prefix-stable, and that provider-native cache hints (e.g.
Anthropic `cache_control`) pass through via the request's passthrough `Extra`
rather than being stripped. Full `cache_control` auto-injection/translation is a
follow-up, not part of this ADR.

## Consequences

- **Schema:** `Route.Strategy` gains the value `"session_affinity"`. A new
  bootstrap/config knob lists candidate session-header names (default
  `["X-Voxeltoad-Session"]`). No new tables.
- **Plumbing (the main blast radius):** the router's candidate resolution gains a
  session-key parameter (`Candidates(alias, sessionKey)`), so `Dispatcher.Forward`
  / `ForwardStream` thread the key through, and the HTTP handler extracts it
  before dispatch. Forwarders, the breaker, and the failover loop are unchanged.
- **Observability:** the resolved session key (or a hash of it — never log raw
  user content) and the affinity decision are candidates for a routing debug
  attribute; the chosen provider is already recorded as `llm.provider`.
- **Testing (test-first, per project rule):** unit tests assert determinism (same
  key → same provider), balanced distribution across many keys, that an unhealthy
  affinity target deterministically fails over to the same backup for a given
  key, and the key-extraction precedence (header > body field > prefix hash). An
  e2e test on the existing harness asserts N requests carrying one session header
  all hit the same mock provider while distinct sessions spread across providers.
- **Not built now:** replica/KV-level prefix routing, `cache_control`
  auto-injection & cross-provider translation, and any shared cross-instance
  affinity store (unnecessary — HRW is already instance-agnostic).
</content>
