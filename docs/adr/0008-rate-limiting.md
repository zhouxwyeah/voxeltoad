# ADR-0008: Rate limiting — RPM+TPM, sliding window, multi-dimensional Limiter, in-memory P0

- Status: Accepted
- Date: 2026-06-30

## Context

The step-0 `MemoryLimiter` is a placeholder: a single global rate/burst token
bucket keyed by an opaque string, with `Allow(ctx, key) bool`. That shape is too
weak for an LLM gateway and conflicts with several committed decisions:

- LLM cost/load is measured in **tokens**, not requests. Limiting only requests
  per second cannot control real load (one long-context request can dwarf a
  hundred short ones). LiteLLM/new-api/Higress all do TPM, not just RPM.
- ADR-0005 established **three tenancy levels**; limits must differ per
  tenant/group/key, which a single construction-time rate cannot express.
- A token-bucket allows bursts, which for an LLM gateway are harmful: a burst is
  forwarded straight to the upstream and trips the upstream's own 429.
- `Allow → bool` loses the `Retry-After` a 429 should carry, and cannot express
  "consume N tokens".

## Decision

### Units — RPM and TPM, TPM via allow-then-debit
Limit both requests-per-minute (**RPM**) and tokens-per-minute (**TPM**).

TPM faces a chicken-and-egg problem: the real token count is only known from the
upstream `usage` after the response, but limiting decides at ingress. We resolve
it with **allow-then-debit**: at ingress, reject only if the dimension is
*already* over its limit; after the response, debit the *actual* usage tokens
into the window. We deliberately do **not** block on a pre-estimate at ingress.

Accepted consequence: with allow-then-debit, callers already near the limit can
momentarily overshoot before debits land. Because the ingress check and any
charge happen under one lock, a single caller overshoots by at most one in-flight
request's tokens; under high concurrency the overshoot can reach roughly the
number of simultaneous in-flight requests (each saw "not yet over" before any
debited). This is an intentional P0 trade-off. Callers that know the cost up
front can instead pass it as `n` to `Allow`, which checks-and-charges
atomically (bounded, no overshoot); hard pre-reservation for the
estimate-unknown case can be added later if overshoot proves material.

Memory is bounded (the step-0 placeholder grew unboundedly): counters are
evicted by an idle-TTL sweep (untouched past the TTL and window empty) and an
LRU cap (least-recently-used evicted beyond maxCounters), so high-cardinality
scopes like rotated/deleted keys do not leak.

RetryAfter is capacity-aware: it accumulates the oldest events' costs until
enough would expire to fit the requested `n`, rather than naively pointing at
the single oldest event (which under-estimated for TPM).

### Algorithm — sliding window
Use a **sliding-window counter** (window total), not a token bucket. It smooths
load toward the upstream (no burst pass-through), matches the "quota within a
window" mental model (cf. new-api's `N per window`), and composes naturally with
TPM debiting. The token-bucket implementation from step 0 is replaced.

### Interface — multi-dimensional, AllowN, Decision result
Redesign `Limiter` so it can express per-dimension limits, token cost, and a
retry hint:

```go
type Decision struct {
    OK         bool
    RetryAfter time.Duration // populated when !OK
    // (optionally) which dimension/limit was hit, for observability
}

type Limiter interface {
    // Allow checks (and reserves) n units against all given dimensions.
    Allow(ctx context.Context, dims []Dimension, n int) (Decision, error)
    // Debit records actual consumption (TPM allow-then-debit reconciliation).
    Debit(ctx context.Context, dims []Dimension, n int) error
}
```

A `Dimension` identifies a scope+metric+limit (e.g. {scope: tenant:acme,
metric: TPM, limit: 100000, window: 1m}). RPM uses `n=1`; TPM debits actual
tokens. The exact shape is finalized test-first in step 4.

### Distribution — in-memory P0, documented limitation
P0 uses the in-memory sliding-window implementation. **This is only correct for
a single data-plane instance.** With N instances each enforces the limit
independently, so the effective limit is multiplied by N. Unlike caching (where
per-instance state is merely less efficient), per-instance rate state is
*wrong* for quota enforcement.

This is accepted for P0 and MUST be documented at the limiter and in operator
docs. A Redis-backed global limiter is the cluster-correct implementation,
added behind the same interface when the data plane scales out. Note this means
that, once multi-instance + money-related limits are both required, Redis ceases
to be optional and becomes required (revisit ADR on Redis being "extension only"
at that point).

## Consequences

- `internal/plugin/ratelimit` is reworked: sliding-window engine + the new
  multi-dimensional interface; step-0 token-bucket tests are replaced.
- RPM enforced at ingress; TPM enforced as allow-then-debit (ingress check +
  post-response debit from real usage — ties into step 6 billing/usage).
- 429 responses carry `Retry-After` from `Decision.RetryAfter`.
- Limits are sourced per tenant/group/key (ADR-0005) from config; wiring the
  config fields is part of step 4/7.
- P0 multi-instance inaccuracy is a known, documented gap, not a silent bug.
