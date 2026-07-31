# ADR-0052: Enterprise budgets and resource accounting

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0012](0012-billing-and-quota.md) (cost and quota), [ADR-0013](0013-quota-data-plane-access.md) (pre-debit/settle), [ADR-0016](0016-data-service-design.md) (multi-scope atomicity), [ADR-0051](0051-application-governance-identity.md) (Application identity)

## Context

The current Quota mechanism is a strongly consistent, non-recurring cost balance
scoped to Tenant, Group, or APIKey. It already reserves an estimated maximum cost
before dispatch and settles to actual upstream Usage after completion. This is a
sound enforcement primitive, but it does not express enterprise calendar budgets,
application/environment scopes, soft thresholds, pre-approved degradation, or a
separate token allowance.

"Compute" also has two different meanings. For provider APIs the authoritative
facts are provider-reported tokens and billed prices. For self-hosted inference,
GPU occupancy, queue time, KV-cache behavior, and accelerator capacity are separate
resource facts that cannot be reconstructed accurately from token counts.

A single generic "token quota" would therefore conflate spend, throughput, and
physical compute while losing the strong consistency guarantees of the current
money path.

## Decision

### Cost is the authoritative enterprise budget unit

Application cost control uses integer micro-unit **Cost Budget** as its primary
hard economic boundary. Actual cost is still computed from provider-reported Usage
and the price of the provider/model that served the request.

A Budget may additionally carry an independent **Token Allowance** for capacity or
contractual controls. Cost and tokens are not interchangeable: different models,
providers, input/output directions, cached tokens, and modalities have different
prices.

RPM, TPM, and concurrency limits remain rate/capacity controls. They are not Budget
accounts and do not share calendar-budget semantics.

### BudgetPolicy and BudgetAccount separate configuration from runtime state

The management model has two concepts:

- **BudgetPolicy** — scope, measure, period, hard/soft behavior, warning thresholds,
  timezone, and overage action;
- **BudgetAccount** — one concrete policy period with limit, reserved, committed,
  released, and period start/end state.

A BudgetPolicy supports calendar `daily`, `weekly`, and `monthly` periods plus
`non_recurring`. Period boundaries are evaluated in the policy's configured
timezone; DST transitions follow that timezone's civil rules. Period rollover
creates or activates a new account; it is not a sliding-window recovery mechanism.
Rollover does not carry over unspent balance unless the policy explicitly defines
a carry-forward rule; the default is zero carry-forward.

Settlement is idempotent: a request may be settled at most once per account, keyed
by its request identity. Late-arriving settlements (e.g. delayed upstream Usage)
charge the account that was active at the request's reservation time, not the
account current at settlement time, so a period boundary never shifts spend
between accounts.

The current Quota balance maps to a non-recurring Cost Budget account. Existing
`TryDebit`/`Settle` behavior is the implementation foundation for reservation and
settlement; this ADR does not weaken its atomicity or fail-closed money semantics.

### Soft and hard budgets have distinct effects

- A **Soft Budget** emits threshold and exceeded events but never blocks a request.
- A **Hard Budget** requires a successful reservation before dispatch.

Hard-budget exhaustion follows a bounded first-phase action sequence:

1. apply an Application-configured, pre-approved degradation tier;
2. re-evaluate the route and reservation under the degraded constraints;
3. reject if no allowed tier fits the remaining budget.

A degradation tier may select a cheaper route or reduce an output ceiling. The
gateway must not invent a substitute model or silently cross an Application's
quality/policy boundary. Budget-driven degradation is recorded separately from
failure-driven failover in the routing decision ledger.

Queueing, human approval, borrowing, and mid-stream truncation are not first-phase
Budget actions. They require Agent/Harness lifecycle semantics.

### All applicable hard budgets form an intersection

A request must satisfy every configured Hard Budget that applies to its context:

- Tenant;
- consuming Group;
- Application;
- Application + environment override;
- APIKey.

All applicable reservations are all-or-nothing in one store transaction. A more
specific policy cannot override or bypass a broader hard boundary. Soft policies
are evaluated independently and only produce events.

Application ownership does not transfer spend: an Application's owning Group is
not charged for requests authenticated by another consuming Group unless it is
also the consuming Group.

This extends ADR-0016's multi-scope transaction from a tenancy-only hierarchy to a
set of orthogonal governance dimensions.

### Token allowance is independent from cost reservation

Token Allowance uses provider-reported actual Usage as the accounting fact. The
first phase does **not** perform a strict pre-dispatch token reservation, because
the gateway has no authoritative prompt tokenizer before dispatch and a fabricated
estimate would be unsafe as a hard gate.

Instead, a Hard Token Allowance is enforced as post-completion accounting: a
completed request debits actual total tokens from the current period account. An
in-flight request is allowed a bounded overage; once the period account is
exhausted, subsequent requests are rejected until the next period rolls over. This
means Token Allowance is a period-level circuit breaker, not a per-request
reservation.

No locally fabricated token count may replace provider-reported Usage in the final
ledger. A future provider-specific tokenizer may tighten this into a reservation
model, but it cannot redefine final accounting.

### Provider billing and self-hosted compute are separate resource sources

Define a conceptual **ResourceUsage** envelope with an explicit source:

- `provider_billed` — provider/model, input/output/cache/reasoning usage where
  available, price version, billed/allocated cost, latency, and status;
- `self_hosted_compute` — deployment/replica, accelerator class, GPU time, queue
  time, KV-cache signals, and internally allocated cost where available.

Only reliable source measurements are recorded. Token count must not be presented
as GPU consumption. Internal cost allocation for self-hosted compute requires an
explicit pricing/allocation rule and must be distinguishable from an external
provider bill.

This phase defines the boundary but implements only provider-billed token and cost
facts. Self-hosted compute collection and resource-aware replica scheduling are
deferred until a real vLLM/llm-d or equivalent inference plane is integrated.

## Consequences

- Application and environment become first-class Cost Budget scopes without
  weakening Tenant/Group/Key caps.
- Enterprise operators can express recurring departmental/application budgets and
  one-off funded balances through one policy/account model.
- Cost, token allowance, rate limiting, and physical compute remain auditable as
  different measures rather than one overloaded quota number.
- The reservation store must persist policy-period identity and reserved/committed/
  released state, and atomically reserve every applicable hard account.
- Budget-driven degradation must run before final reservation and routing, and its
  decision/reason must be queryable.
- Period rollover, idempotency, concurrency, and late settlement need explicit
  implementation tests before schema work lands.
- Self-hosted GPU accounting does not block the provider-API enterprise roadmap,
  while the event boundary avoids redesign when that integration becomes real.

## Alternatives considered

### Token as the primary budget

Rejected because raw tokens are not economically comparable across models,
providers, directions, modalities, and cache/reasoning pricing.

### Cost and tokens as one fungible account

Rejected because conversion would depend on a route and price not known at ingress
and would hide whether a limit protects spend or capacity.

### Keep only the current non-recurring Quota balance

Rejected because external jobs would need to top up every calendar period and the
management plane could not express soft thresholds or period-specific reporting.

### Replace Quota with calendar budgets only

Rejected because prepaid, project-specific, and one-off funded allowances still
need a non-recurring account.

### Most-specific policy overrides parent policy

Rejected because an Application or APIKey policy could then bypass a Tenant or
Group hard budget.

### Full overage workflow engine in the gateway

Rejected for the first phase. Queueing, approval, borrowing, and callbacks belong
to a broader Harness lifecycle rather than the synchronous model gateway path.

### Infer GPU cost from tokens

Rejected because batching, hardware, quantization, queueing, cache reuse, and
serving implementation make token count an unreliable proxy for physical compute.
