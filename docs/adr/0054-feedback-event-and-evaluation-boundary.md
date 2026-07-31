# ADR-0054: FeedbackEvent and the evaluation boundary

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0021](0021-request-logs-data-plane-audit-ledger.md) (request ledger), [ADR-0039](0039-llm-trace-payload-capture.md) (trace payloads), [ADR-0051](0051-application-governance-identity.md) (Application), [ADR-0053](0053-explainable-application-routing.md) (RoutingDecision)

## Context

Routing decisions, usage, and request logs describe what happened on the gateway
side, but not whether the outcome was good. Enterprise routing optimization,
budget degradation tuning, model selection, and evaluation datasets all depend on
outcome signals: did the response satisfy the user, pass a verifier, or achieve a
business result?

Without a structured feedback channel, outcome data either never reaches the
gateway or arrives as ad-hoc fields scattered across external systems with no
lineage back to the request, policy version, or application that produced it. That
breaks the closed loop between decision and outcome that later stages of routing
maturity require.

Building a full evaluation platform inside the gateway — judges, experiment
tracking, leaderboards, dataset curation, training pipelines — would instead
expand the gateway far beyond its governance and accounting role and duplicate
existing evaluation tooling.

## Decision

### FeedbackEvent is an immutable, tenant-scoped metadata ledger

A **FeedbackEvent** is an append-only record associated with a gateway request,
session, or application context. It carries:

- `request_id` and/or `session_id` association to the original gateway event;
- `application_id` and `environment` snapshot;
- `metric` — e.g. `success`, `correctness`, `preference`, `business_value`,
  `task_completion`;
- `value` — numeric score, categorical label, or boolean;
- `source` — `human`, `business_system`, `deterministic_verifier`, or `judge`;
- `trust_level` — distinguishing deterministic verifiers from subjective or
  model-based judgment;
- `evaluator_version` — the version of the verifier, judge model, or rubric;
- `evidence_ref` — optional pointer to an external evidence artifact or dataset
  item, not an inline body;
- `observed_at` — when the feedback was produced.

FeedbackEvents do not copy prompt or completion bodies by default. They reference
trace payloads or dataset items by identifier when evidence is needed.

### The gateway owns the ledger and lineage, not the evaluation execution

The gateway provides:

- authenticated ingestion endpoints for feedback;
- authorization scoped to the Application or Tenant that owns the original
  request;
- immutable storage and lineage join to `request_logs`, `usage_records`, and
  `RoutingDecision`;
- query and export APIs for offline analysis.

The gateway does not provide:

- LLM-as-judge execution;
- experiment assignment, A/B tracking, or leaderboard computation;
- dataset curation, training pipelines, or model fine-tuning;
- automated policy rewrite from feedback without a separate human-approved
  decision.

External evaluators, Harness runners, or business systems produce feedback and
send it to the gateway. The gateway is the factual substrate, not the evaluator.

### Trust levels prevent mixing signal types

A deterministic verifier result (e.g. tests passed, code compiled, task completed)
has higher trust than a subjective human preference or an LLM judge score. Trust
level is mandatory metadata so downstream consumers can filter, weight, or exclude
signal sources without guessing.

Judge-model feedback must record the judge model and version; it cannot be
silently relabeled as ground truth.

### Feedback is append-only and cannot rewrite history

FeedbackEvents supplement but never mutate `usage_records`, `request_logs`, or
`RoutingDecision`. A negative feedback does not retroactively change a billed
cost or a recorded routing outcome. The relationship is referential: feedback
explains the quality of an event that the other ledgers already recorded as a
fact.

## Consequences

- The gateway becomes the single join point between routing decisions, cost, and
  outcome quality without becoming an evaluation platform.
- Offline evaluation, A/B analysis, and future learned routing have a stable,
  queryable feedback substrate with explicit trust levels.
- Feedback ingestion must be authenticated and tenant-scoped; unauthenticated
  feedback is not accepted.
- The ledger adds storage and retention requirements, but metadata-only events
  are bounded and do not carry the cost or privacy weight of trace payloads.
- Feedback does not automatically change production routing; translating feedback
  into policy changes requires a human-approved decision or a separate learned
  routing ADR.

## Alternatives considered

### Built-in evaluation platform

Rejected for this phase. It would require judges, experiment tracking, dataset
curation, and leaderboards — a separate product surface that duplicates existing
evaluation tooling and expands the gateway beyond governance and accounting.

### Feedback as free-form request log fields

Rejected because request logs are per-request audit facts, not an open-ended
append-only evaluation ledger. Mixing them would bloat the audit row and lose
trust-level and source lineage.

### Export-only (no ingestion)

Rejected because exporting gateway events to an external system and re-importing
feedback would break lineage: the gateway would not know which requests received
what outcome, making closed-loop analysis and future learned routing impossible.

### Feedback rewrites billing

Rejected. Cost settlement is a money-path fact based on provider-reported Usage.
Feedback may inform future policy but must not retroactively mutate billed cost;
chargeback adjustments, if needed, are a separate financial operation.
