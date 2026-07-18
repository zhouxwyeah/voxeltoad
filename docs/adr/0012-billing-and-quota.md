# ADR-0012: Billing & quota — cost units, quota vs rate limit, partial-stream billing, consistency

- Status: Accepted
- Date: 2026-06-30
- Supersedes the open question in ADR-0004 (pricing granularity).

## Context

Step 6 adds billing (recording cost per request) and quota (stopping a
tenant/group/key when its balance is exhausted). Several things interact and are
easy to get wrong: quota overlaps with TPM rate limiting, cost can't be known at
ingress, streams can drop before usage arrives, and quota is real money so
multi-instance inaccuracy is unacceptable.

Existing pieces this builds on: `adapter.Usage` (prompt/completion/total from the
upstream), `ModelUpstream.Pricing` (per-provider per-million-token rates, already moved
per-provider in step 5 — which resolves ADR-0004), the rate limiter's
allow-then-debit, and `plugin.Context` (Tenant/Group/APIKeyID/Provider/Response).

## Decision

### Quota is independent of rate limiting
Rate limit (TPM, ADR-0008) bounds a **rate** (tokens per sliding window,
self-recovering). Quota bounds a **balance** (total spend until reset/top-up,
NOT self-recovering). They are separate mechanisms — quota is not a
long-window rate limiter (a sliding window would wrongly "recover" spent
balance). Both use allow-then-debit at ingress/post, but against different
state.

### Cost is money/points, not raw tokens
Define a unit cost:

```
cost = prompt_tokens/1_000_000 * Pricing.PromptPer1M
     + completion_tokens/1_000_000 * Pricing.CompletionPer1M
```

using the **actually-hit provider's** `ModelUpstream.Pricing` (aligns with
`llm.provider`; failover bills the provider that served). Quota balances are
denominated in this cost (money), not token counts, because prompt/completion
and different providers have different rates — raw tokens aren't equivalent
across them. This mirrors new-api's "points" abstraction.

Ingress quota check is allow-then-debit like TPM: at ingress reject only if the
balance is already ≤ 0; after the response, debit the real cost. (Cost is
unknowable at ingress — same chicken-and-egg as ADR-0008.)

### Partial-stream billing: bill what was received
When a stream drops before the trailing usage chunk, bill the **usage actually
received** (input tokens are known from message_start/the request; output tokens
= what was streamed so far). Best-effort, never zero when content was delivered,
never fabricated. The proxy already terminates such streams cleanly (step 3.5);
billing hooks the same completion point.

### Quota consistency: shared store, strong debit (P0)

> **Refined by ADR-0013.** The "atomic decrement … overspend is not acceptable"
> and check-then-debit (`Exceeded`/`Debit`) wording below is superseded by
> ADR-0013: data plane debits PG directly via **pre-debit/settle** (`TryDebit`/
> `Settle`), overshoot is **bounded** (in-flight estimate error) not zero, and
> money is **int64 micro-units**. The paragraph is kept for historical context.

Unlike rate limiting (in-memory, per-instance, documented gap), **quota is money
and MUST be consistent across instances from P0**: balance debits go to a shared
store with atomic decrement (PostgreSQL row update or Redis atomic). Multi-
instance overspend is not acceptable. This makes a shared backend a P0
requirement for quota — the first feature to require one — explicitly diverging
from the "in-memory default, Redis optional" baseline (revisit ADR-0008's
optional-Redis stance accordingly).

Billing **records** (audit/reconciliation, `usage_records`) are separate from
quota debits: records can be written async/batched to PG (slow path, may lag);
quota debits are the fast, consistent path. A crash may lose a not-yet-flushed
record (under-billing) but must not corrupt the quota balance.

## Required prerequisites (ordering)

- **Plugin chain must run inside the forward path.** Billing/quota debit happens
  on response completion (Post phase). The plugin chain is not yet wired into
  proxy.Forward/ForwardStream (deferred since step 4). Step 6 must wire a
  completion hook (non-streaming: after Forward; streaming: when the relay loop
  ends, including on drop) before billing can land.
- **Quota's shared store ties step 6 to step 7's PostgreSQL** (or a Redis
  decision). Quota balance + `usage_records` need persistence; the in-memory
  shortcut used elsewhere is not allowed for balances.

## Implementation order (decided)

The dependency on PostgreSQL is on the *persistence backend*, not on the billing
logic. Billing/quota split into "trigger" (pure data-plane) and "persist" (PG),
so the order is **hook-first, PG-last** — the same interface-then-backend pattern
already used for `auth.KeyStore`, `ratelimit.Limiter`, `cache.Cache`:

1. **Completion hook** (pays off the step-4 deferral): run the plugin chain's
   Post phase at the response-completion point in Forward/ForwardStream
   (streaming: when the relay loop ends, including on drop). This activates the
   already-written-but-uncalled `ratelimit.Plugin.Debit` (TPM allow-then-debit
   closes). Pure data-plane, no PG.
2. **Step 6 pure logic**: a `billing` package computing cost from usage + the hit
   provider's pricing; `QuotaStore` and `UsageRecorder` *interfaces* with
   in-memory implementations (test-only); quota check (Pre) + debit (Post) wired
   through the hook. No PG.
3. **Step 7**: PostgreSQL repositories implementing `auth.KeyStore`,
   `QuotaStore`, `UsageRecorder`, plus snapshot serialization and admin CRUD.

The in-memory `QuotaStore` is explicitly test-only: per ADR-0012, production
quota MUST use the consistent PG/Redis implementation (the in-memory one would
overspend across instances). This is documented at the interface.

## Consequences

- ADR-0004 resolved: pricing is per-provider (`ModelUpstream.Pricing`); billing
  uses the hit provider's rate.
- Two debit paths on each completion: TPM rate (in-memory) and quota balance
  (shared store) — plus an async billing record. Their triggering is unified at
  the single completion hook to keep them consistent.
- Quota introduces a P0 hard dependency on a consistent store, diverging from
  the in-memory-default baseline; documented and intentional.
- Partial-stream billing is best-effort on received usage.
