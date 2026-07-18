# ADR-0013: Quota data-plane access — direct PG, pre-debit/settle, integer micro-units

- Status: Accepted
- Date: 2026-06-30
- Refines ADR-0012 (billing & quota): replaces its "atomic decrement, overspend
  not acceptable, check-then-debit" wording with the concrete access model,
  consistency semantics, and value representation below.
- Relates to ADR-0005 (tenancy), ADR-0006 (key auth / data channel), ADR-0007
  (control-plane trust), ADR-0008 (rate limiting).

## Context

Step 7 must make quota real. ADR-0012 settled *that* quota needs a consistent
shared store and is billed allow-then-debit, but left three things open that the
schema and the `QuotaStore` interface both depend on:

1. **How does the data plane reach quota state?** The architecture says the data
   plane is stateless and reads only the config snapshot (ADR-0007). But quota is
   read/written on the request hot path. Either the data plane gains a stateful
   dependency, or the management plane goes on the critical path. This must be
   stated, not left implied.
2. **What consistency does "no overspend" actually mean?** ADR-0012 says
   "multi-instance overspend is not acceptable", yet allow-then-debit and
   unknown-cost-at-ingress make *zero* overshoot impossible without truncating
   in-flight responses. The current `Exceeded()` then `Debit()` interface is
   check-then-act and races under concurrency.
3. **What type is money?** `Pricing.*`, `Cost()`, `QuotaStore.Debit(float64)`
   and `UsageRecord.Cost` are `float64`. Quota is money; repeated
   `balance -= 0.0001` accumulates IEEE-754 drift, and PG `DOUBLE PRECISION`
   inherits it.

Surveyed comparable gateways:

- **LiteLLM**: pre-request `reserve_budget_for_request()` reserves the
  *estimated max cost* against a Redis/in-memory counter (blocks if
  `spend + estimate > budget`); post-request `reconcile`/`release`; spend logs
  batched async to Postgres; `pod_lock_manager` (Redis lock) dedupes
  cross-instance writes. Strong at the decision point, eventual for the ledger.
  Requires Redis for the shared counter.
- **new-api / one-api**: 预扣费 — estimate cost, atomically decrement the quota
  row in SQL (`UPDATE ... WHERE quota >= est`), reject (402) if insufficient and
  never forward; post-request 多退少补 — settle to the real cost (refund or
  charge the difference) on the same row. Quota lives in MySQL/PG; **the atomic
  SQL row update is the cross-instance consistency primitive — no Redis.** Money
  is stored as integers (单价 × 10000) to avoid float drift. Explicitly accepts
  that a near-empty account can still produce one over-budget long reply (the
  gateway does not cut a response mid-stream).

Both confirm: keys go through cache/snapshot (eventual, TTL-bounded — already
ADR-0006); quota is a separate, consistent path. The two designs differ only in
*where the atomic primitive lives* (Redis counter vs SQL row) and *whether the
estimate is reserved or debited*.

## Decision

### 1. Data plane reaches the quota store directly; it is the one synchronous stateful dependency

The data plane is **not** purely stateless. It has exactly **one** synchronous
stateful dependency on the request path: the **quota store (PostgreSQL)**.
Everything else stays as designed — config via snapshot/cache (ADR-0007), keys
via cache-first + store fallback (ADR-0006), rate limit in-memory per-instance
(ADR-0008), usage records written async (below).

Rejected alternatives:

- **Data plane → management-plane API for quota.** Keeps the "stateless data
  plane" diagram literally true, but puts the admin plane on the request
  critical path: a latency-adding SPOF and an RPC layer reinvented over our own
  DB. Rejected.
- **Redis atomic counter (LiteLLM model).** Works, but adds Redis as a P0
  dependency, contradicting "PostgreSQL is the only stateful dependency" for
  phase-1 VM deployment. Deferred to phase 2 (see Consequences); the `QuotaStore`
  interface keeps it swappable.

`design/architecture.md` is updated to say: the data plane reads config from the
snapshot channel **and** holds a direct, pooled connection to the quota store for
atomic debit. Keys/usage do not need this (cache / async). The quota-store **DSN
is bootstrap config / env-sourced** (like the `X-Internal-Token`, ADR-0015 §4),
not obtained from the DB or the snapshot.

### 2. Pre-debit the estimate, settle to actual — bounded overshoot, not zero

Quota uses **pre-debit → settle** (new-api's 预扣 / 多退少补), with the SQL row
as the consistency primitive:

- **Pre (ingress):** estimate the request's max cost, then **atomically
  conditionally debit** it:
  `UPDATE quotas SET balance = balance - :est WHERE scope = :s AND balance >= :est`.
  Zero rows affected ⇒ insufficient balance ⇒ reject (HTTP 402, mapped at the
  proxy) and **do not forward**. This is safer than forward-then-reconcile and
  is the single atomic check-and-act that closes the race.

  **Estimate basis (ordering + tooling constraint):** the Pre phase runs
  *before* dispatch/failover, so **the hit provider is not yet known**; and the
  gateway has **no prompt tokenizer** (`pkg/tokenizer` is a stub), so prompt
  tokens are *not* countable at ingress. The reservation is therefore a
  **completion-only ceiling**: `est = effectiveMaxTokens × (max completion rate
  across the alias's candidate providers)`, where `effectiveMaxTokens` is the
  request's `max_tokens` if set, else the **max `DefaultMaxTokens` across the
  alias's candidates**, else a configured global ceiling. Prompt cost is *not*
  reserved — it is charged at Post against the **exact** actual usage, so the
  final balance is always correct; the only effect is that the transient
  overshoot bound grows by one in-flight request's prompt cost (still bounded,
  per the guarantee below). Conservative on the completion side (max candidate
  rate) so failover to a pricier provider never under-reserves the completion.
- **Post (completion):** compute the real cost from actual usage at the **hit
  provider's** pricing (now known; partial-stream rules from ADR-0012 unchanged)
  and **settle the difference**:
  `UPDATE quotas SET balance = balance + (:est - :actual)` (a refund when
  actual < estimate, an extra charge when actual > estimate, e.g. a long stream).
  Settlement is **unconditional** and **always runs** — it reconciles a
  reservation we already made. **On total failure / no usage** (all providers
  failed, stream delivered nothing) `actual = 0`, so Post issues a **full refund**
  (`delta = est`); it is never a no-op (that would leak the reservation forever).
  This is a behavioural change from the old check-then-debit model, where
  no-usage correctly charged nothing — under pre-debit, no-usage must refund.

**Consistency guarantee (the honest one):** balance can go transiently negative
by at most the estimate error of the requests *currently in flight on that
scope* — it never compounds across completed requests, and never drifts across
instances (every mutation is one atomic SQL statement). A near-empty scope may
still serve one over-budget long reply; we do **not** truncate a response
mid-stream to hit an exact ceiling. This matches every surveyed gateway.

This **supersedes ADR-0012's** "atomic decrement … overspend is not acceptable"
and its check-then-debit (`Exceeded()` then `Debit()`) framing. The
`QuotaStore` interface changes accordingly — illustrative, final signatures
land test-first in step 7:

```go
type QuotaStore interface {
    // TryDebit conditionally debits est from the scope's balance in one atomic
    // step. ok=false (no error) means insufficient balance → reject. Unconfigured
    // scopes are unlimited (ok=true, no row touched).
    TryDebit(ctx context.Context, scope string, est int64) (ok bool, err error)
    // Settle reconciles a prior reservation: delta = est - actual (positive =
    // refund, negative = extra charge). Unconditional.
    Settle(ctx context.Context, scope string, delta int64) error
}
```

> **Signature refined by ADR-0016.** The single-`scope` form above cannot give
> all-or-nothing semantics across the caller's tenant/group/key scopes. ADR-0016
> changes both methods to take a **`scopes []string`** set so the multi-scope
> transaction boundary lives in the store. Implement the ADR-0016 form.

The Pre/Post split already exists in the billing plugin (ADR-0012); Pre calls
`TryDebit`, Post calls `Settle` (always — see the full-refund rule above). The
old `Exceeded`/`Debit` pair is removed.

**Rejection status propagation:** ADR-0012's plugin contract only has
`Context.Stop`, which the router maps to a single code (429). Quota needs to
distinguish **402** (insufficient balance) from **503/502** (store unreachable,
fail-closed) from the rate limiter's **429**. `plugin.Context` therefore gains a
rejection-status field (e.g. `RejectStatus int` / a typed reject reason) that the
router reads to emit the correct code. This small contract addition lands with
the billing rework (test-first).

### 3. Money is integer micro-units end-to-end

All monetary values — pricing rates, cost, balances, deltas — are **`int64`
micro-units** (a fixed scaling factor applied to the configured currency unit;
the unit is operator-defined, the gateway does no currency semantics). Stored as
`BIGINT` in PG, computed with integer arithmetic, divided back only for display.
This eliminates float drift across millions of small debits (new-api's explicit
"乘以 10000 存整数" lesson).

Interface impact (lands test-first in step 7): `Pricing.PromptPer1M` /
`CompletionPer1M`, `billing.Cost(...)`, `QuotaStore` est/delta, and
`UsageRecord.Cost` move from `float64` to `int64` micro-units. The scaling
factor is fixed and documented at one place (a `billing` constant); config YAML
may accept human-readable decimals and convert on load.

Two arithmetic details for the implementation: (a) **rounding** — `cost =
tokens/1_000_000 × rate` in integer micro-units must use a single stated rounding rule
(round-half-up) applied once at the end, so `Cost()` is deterministic; (b)
**currency** — a scope's balance and the pricing debited against it are assumed
to share one currency (single-currency-per-deployment in phase 1); `currency` is
a label only and cross-currency debit is not validated. Mixed-currency support is
out of scope.

## Consequences

- The data plane gains a PG connection pool for quota. **When the quota store is
  unreachable, the data plane fails closed**: the request is rejected (mapped to
  HTTP 503/502 at the proxy), not served. Quota is money — serving on a quota
  outage means uncontrolled spend, which is worse than a transient availability
  hit. This makes the quota store a hard P0 dependency for any scope that has a
  configured balance; unconfigured (unlimited) scopes are unaffected only if the
  "unconfigured = unlimited" check itself does not require a reachable store
  (i.e. failure to *determine* configured-ness also fails closed). Retries/
  timeouts on the PG call are an implementation detail of step 7.
- `QuotaStore` interface is reshaped (`TryDebit`/`Settle`, `int64`) and the
  in-memory test implementation, the billing plugin's Pre/Post calls, and all
  affected tests change with it — test-first, one focused commit, `make ci` +
  `make test-db` green.
- The `quotas` table needs a row-level atomic conditional update; its scope
  identity (string key vs FK columns) is part of the schema grill (§1) and is
  **not** decided here. The balance column is `BIGINT` micro-units.
- ADR-0012's quota-consistency paragraph is refined by this ADR; its
  cost-formula, quota-vs-rate-limit, partial-stream, and async-records decisions
  stand unchanged.
- Redis remains a phase-2 swap behind the same `QuotaStore` interface (LiteLLM
  model) if/when multi-region or extreme write contention demands it. ADR-0008's
  "optional Redis" stance is unaffected — rate limit stays in-memory.
- Usage records (`UsageRecorder`) remain async/batched to PG, lossy-on-crash,
  separate from the quota path (ADR-0012) — unchanged.
