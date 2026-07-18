# ADR-0011: Routing strategies and failover semantics

- Status: Accepted
- Date: 2026-06-30

## Context

Routing resolves a model alias to a provider (ADR-0002) and may fail over to
backups. LLM failover differs from generic HTTP retry: some errors must not be
retried, streaming cannot be transparently retried after bytes are sent, and
retries can double-bill. The step-0 `Route{ModelAlias, Providers[], Strategy}`
needs concrete failover semantics, and a few schema gaps surfaced.

## Decision

### Failover trigger — whitelist only
Fail over only on **retryable upstream failures**: connection errors, timeouts,
and 5xx. Do **not** fail over on 4xx — especially 400 (e.g. content moderation,
malformed) and 429 (rate limit): retrying another provider wastes tokens and
usually fails again. 401/403 (provider credential issues) are operational, not
retryable per-request.

### Streaming — fail over only before the first byte
For streaming requests, failover is allowed **only before the first chunk has
been written to the client**. Once any chunk is sent, a mid-stream upstream
failure cannot be retried on a backup (the client already holds a partial,
in-voice answer; replaying would duplicate/garble it). After first byte, the
error is propagated and the client stream is terminated cleanly (`[DONE]`, per
step 3.5). Non-streaming has no such constraint (nothing sent until complete).

### Billing on failed attempts
A failed attempt that consumed no usable response is **not billed** (no usable
`usage`). When failover succeeds on a backup, billing uses the **actually-hit
provider** (consistent with ADR-0004/observability `llm.provider`). A provider
that returned a billable error with usage is an edge case deferred to step 6.

### Schema additions (step 5)
- **Per-route weights**: add weights to `Route` (e.g. `Providers` becomes a list
  of {name, weight}) so the same provider can carry different weights in
  different routes; `Provider.Weight` remains a default.
- **Health/circuit state**: failover skips providers currently marked unhealthy.
  This circuit state is **in-memory in the data plane for P0** — and, like rate
  limiting (ADR-0008), is therefore per-instance and not globally consistent
  under multiple replicas. Documented limitation; a shared store is the
  multi-instance upgrade.

### Strategies
`priority` (ordered, first healthy wins), `weighted` (by route/provider weight),
`round_robin` (cursor; per-instance under multiple replicas — acceptable for P0).
A `session_affinity` strategy (same session → same provider, for provider-side
prompt-cache locality) is added later in ADR-0018, orthogonal to the failover
semantics decided here.

## Consequences

- Failover is safe: no wasted retries on non-retryable errors, no garbled
  streams, no double-bill of failed attempts.
- `Route` schema gains per-provider weights; circuit state is a new in-memory
  structure with a documented multi-instance gap (mirrors ADR-0008).
- The "first byte" boundary ties failover to the streaming relay (step 3.5):
  see the implementation design below — it falls naturally on whether
  `ForwardStream` returned a reader, so no extra "bytes sent" flag is needed.
- Round-robin / circuit per-instance inconsistency is an accepted P0 trade-off,
  revisited alongside the ADR-0008 Redis decision when scaling out.

## Implementation design (step 5)

Three layers, keeping the existing single-provider `Forwarder` intact:

```
handler → Dispatcher (route + failover) → Forwarder (single provider) → Adapter
```

- **Forwarder** stays a single-provider executor (one adapter + timeouts +
  http.Client). Only change: split the upstream error so 4xx (non-retryable)
  is distinguished from 5xx (retryable) — today both collapse into errUpstream.
- **Dispatcher** orchestrates: resolves the route's ordered candidates (via
  Router, filtered by the circuit breaker), then tries each in turn. It returns
  the unified response/stream plus the actually-hit provider (for billing /
  `llm.provider`). Holds a `map[providerName]*Forwarder`, rebuilt on config
  change (P0: built once at wiring).
- **Router** (pure, table-tested): orders candidates by strategy
  (priority/weighted/round_robin) and drops breaker-unhealthy ones.
- **circuitBreaker** (in-memory, ADR-0011 gap): consecutive-failure count marks
  a provider unhealthy with a cooldown (time-based half-open); success resets.

**The first-byte boundary is structural, not a flag.** `ForwardStream` checks
the upstream status and returns the `StreamReader` *before* any byte is written
to the client (the handler only starts `Recv`+write after it has the reader).
So failover = retrying the `ForwardStream` *call* across candidates; once a call
succeeds and returns a reader, that provider is locked and any later `Recv`
error propagates (the handler may have written). Non-streaming is the same on
`Forward`. The Forwarder needs no "bytes sent" awareness.

**Retry decision** uses the refined `upstreamError.kind`: `errTimeout` and
`errUpstream5xx` (and connection errors) are retryable; `errUpstream4xx`
(moderation/malformed/429) and `errBuild` are not — matching the whitelist.
