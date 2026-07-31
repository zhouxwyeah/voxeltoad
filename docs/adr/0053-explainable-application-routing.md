# ADR-0053: Explainable Application routing and decision ledger

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0011](0011-routing-and-failover.md) (routing/failover), [ADR-0021](0021-request-logs-data-plane-audit-ledger.md) (request ledger), [ADR-0051](0051-application-governance-identity.md) (Application), [ADR-0052](0052-enterprise-budgets-and-resource-accounting.md) (budget degradation)

## Context

The current Router orders providers with priority, weighted, round-robin, or
session-affinity strategies and filters breaker-unhealthy endpoints. Request logs
record the final provider, endpoint, latency, error, and fallback flag, but not the
candidate set, policy version, eligibility filters, signal values, or selection
reason.

That is sufficient for static forwarding but insufficient for application-specific
cost/quality/latency policy, budget-driven degradation, decision replay, A/B
comparison, or future learning. Introducing semantic or learned routing before a
stable decision event would create an online black box with no reliable evaluation
or rollback basis.

External market benchmarks can help cold start, but enterprise tasks, network
paths, provider accounts, regional policy, and quality expectations differ. The
gateway needs private operational evidence without treating traffic volume as a
quality label.

## Decision

### Evolve routing in explicit maturity stages

Routing evolves in this order:

1. **Decision Ledger** — capture policy, candidates, reasons, signals, attempts,
   and outcome;
2. **deterministic rules and cascades** — application-approved constraint filters,
   quality tiers, and ordered objectives;
3. **semantic routing** — task classification or embedding-based selection after
   an evaluation set and privacy policy exist;
4. **learned routing** — trained scoring, controlled experiments, and rollback
   after sufficient outcome feedback exists.

The first enterprise routing phase implements stages 1 and 2. Semantic and learned
routing are not placed on the production critical path yet.

### RoutingPolicy belongs to Application

An Application owns a default **RoutingPolicy** and may define an environment
override. A policy is versioned and immutable once used by a request; edits create
a new version so decisions remain replayable.

The policy evaluates candidates in this ordered sequence:

1. **Eligibility filtering (hard constraints)** — model access, protocol/parameter
   capability, context/output limits, data handling/residency policy, explicit
   provider/model allow/deny rules, and budget price ceiling;
2. **Quality tier** — an operator-approved candidate class such as `economy`,
   `balanced`, or `premium`;
3. **Ordered objective** — an explicit order such as `cost_first`,
   `latency_first`, or `quality_first` rather than an arbitrary cross-unit weighted
   sum;
4. **Budget-driven degradation re-evaluation** — if ADR-0052 budget pressure
   applies, select another Application-approved tier or output ceiling and
   re-filter candidates before reservation;
5. **Atomic reservation** — reserve every applicable hard budget account in one
   transaction;
6. **Health and execution** — breaker health, candidate attempt order, and the
   existing ADR-0011 failover semantics.

Hard constraints remove candidates and record reasons. Soft objectives only order
remaining candidates. A more favorable price or latency cannot bypass a hard
policy constraint.

Budget-driven degradation from ADR-0052 selects another Application-approved tier
or output ceiling before final reservation. It is a policy transition, not a
failure retry, and is recorded separately from failover.

### Routing signals have explicit authorities

Signal sources are separated:

- model/endpoint capability, quality tier, data policy, and residency are
  operator-approved catalog facts;
- price and price version come from the configured provider/model price catalog;
- TTFT, throughput, reliability, and error rate come from the deployment's own
  rolling telemetry;
- quality initially comes from approved tiers and offline evaluation, never from
  traffic, popularity, price, or latency;
- external benchmarks and market data may seed a cold start but cannot change a
  production route automatically.

Dynamic performance signals require a minimum sample count, an explicit rolling
window, and a staleness fallback before they may affect ordering. Exact thresholds
are implementation/configuration details, but every used value and its observation
window must be present in the decision snapshot.

### RoutingDecision is an independent immutable ledger

Each routed request produces one metadata-only **RoutingDecision** linked to the
canonical gateway `request_id`. It contains at least:

- Application and environment identity;
- RoutingPolicy id and version;
- requested alias, effective quality tier, hard constraints, and ordered objective;
- every considered model/provider/endpoint candidate;
- candidate eligibility and rejection reasons;
- relevant price version and telemetry/evaluation signal snapshot;
- selected candidate and selection reason;
- any budget-driven degradation;
- ordered attempts and final routing outcome.

The ledger does not contain prompt or completion bodies. Workload context included
in the decision is a bounded, non-sensitive routing summary rather than raw input.
Prompt-derived semantic features require the later semantic-routing privacy decision.

RoutingDecision is separate from `request_logs`:

- `request_logs` remains the final per-request business/audit fact;
- RoutingDecision explains how the target was selected;
- both join through `request_id`.

The database layer does not assume `request_id` is globally unique; the canonical
join key for historical detail retrieval is the ledger row id, with `request_id`
as a secondary lookup index. This avoids relying on a uniqueness guarantee the
current schema does not enforce.

Decision recording is asynchronous and fail-open so analytics storage cannot block
the data plane. Dropped decisions must be counted and observable. A missing decision
must never change billing settlement or the request log's final facts.

### Preserve existing failover safety

This ADR does not change ADR-0011:

- only connection errors, timeouts, and 5xx are retryable;
- 4xx does not trigger provider failover;
- streams fail over only before the first client-visible byte;
- billing uses the provider that actually served billable output.

RoutingPolicy chooses and orders eligible candidates; Dispatcher and Forwarder keep
execution/failover responsibilities.

## Consequences

- Applications can express different cost, quality, latency, capability, and data
  constraints without forking global routes.
- Operators can explain why a candidate was selected or rejected at the policy
  version and signal snapshot used at request time.
- Budget degradation, policy selection, breaker filtering, and execution failure
  become distinguishable events.
- Private telemetry provides useful local routing evidence while approved quality
  tiers prevent latency or popularity from masquerading as answer quality.
- The decision ledger becomes the factual substrate for offline replay, policy A/B
  tests, later semantic classifiers, and learned routers.
- New schemas and retention policy will be required for policy versions and routing
  decisions, but prompt/response privacy remains isolated from this metadata ledger.
- Dynamic routing must provide deterministic stale/low-sample fallbacks to avoid
  amplifying noisy measurements.

## Alternatives considered

### Add semantic routing in the first phase

Deferred. It requires task labels, privacy rules for prompt-derived features,
offline evaluation, and a failure fallback that do not exist yet.

### Start with a learned multi-objective router

Rejected for the first phase. There is no stable outcome label or replayable
training event, so reported optimization would be ungrounded.

### Arbitrary weighted multi-objective scoring

Rejected initially. Combining quality, price, latency, and reliability into one
number introduces normalization and weight-drift problems that are harder to audit
than constraint filtering plus an ordered objective.

### Use external market telemetry as the production authority

Rejected because it does not represent the enterprise's network, provider account,
task distribution, policy, or quality bar. It remains a cold-start input only.

### Add a decision JSON field to request_logs

Rejected because request audit facts and potentially large strategy-debug records
have different schemas, query patterns, and retention needs.

### Emit only OTel events

Rejected as the sole record. Sampled or backend-specific telemetry is insufficient
for stable replay, policy comparison, and curated training/evaluation datasets.
