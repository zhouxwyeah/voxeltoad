# ADR-0046: Persist `ingress_protocol` to the audit ledgers

- Status: Accepted
- Date: 2026-07-22
- Supersedes: the "not persisted to the audit ledger"附带决定 in [ADR-0045](0045-anthropic-ingress-protocol.md) §gap (the field was telemetry-only / OTel span attribute)

## Context

ADR-0045 shipped Anthropic ingress support with `llm.ingress.protocol` as an **OTel span attribute only** — deliberately not written to the `request_logs` / `trace_payloads` ledgers. The rationale was low-cardinality (2 values) and "audit value lower than agent_type."

Operational review surfaced two gaps with that choice:

1. **No historical queryability.** OTel span attributes are only queryable while the trace is retained (hours-days). The management UI's request-logs filtering, session timelines, and cost attribution cannot slice by ingress protocol — operators cannot answer "how much of yesterday's Claude Code traffic was served via direct passthrough vs. translated" without a persisted column.
2. **No filter dimension.** The request-logs list and trace views already filter/group by `agent_type` (migration 00023). `ingress_protocol` is the same *class* of dimension — an operator-facing classification that drives filtering and attribution, not a diagnostic counter.

## Decision

Persist `ingress_protocol` to both audit ledgers (`request_logs` + `trace_payloads`), mirroring `agent_type` (migration 00023). The field was already collected by the data plane (`telemetryAcc.ingressProtocol`, router.go; filled into `RequestTelemetry` for the OTel span) — this ADR only adds it to the two `Record` calls that write the audit ledgers.

Column: `ingress_protocol VARCHAR NOT NULL DEFAULT ''` (empty = pre-migration rows). Value domain: `'' | 'openai' | 'anthropic'`. Both tables are RANGE-partitioned by `created_at`; ADD COLUMN propagates to all partitions automatically.

## Why follow `agent_type` (persisted) and not `RetryCount` (telemetry-only)

`RetryCount` is a diagnostic counter — useful in a live trace view ("this request retried twice then succeeded") but not an operator filter/attribution dimension (nobody asks "show me all requests with retry_count=2 over the last week"). It stays OTel-only.

`agent_type` and `ingress_protocol` are both **operator-facing classification dimensions** that drive:
- request-logs list filtering (`?agent_type=claude-code`, `?ingress_protocol=anthropic`)
- session timeline grouping
- cost/usage attribution ("how much did Claude Code cost via Anthropic-ingress vs OpenAI-ingress")
- the passthrough/translated badge (ingress_protocol × hit-provider adapter)

These use cases require querying historical data, which means the column must be in the audit ledger, not just the trace span.

## Consequences

- One migration (`00025_ingress_protocol.sql`), 15-site full-stack change mirroring `agent_type` (migration → store INSERT/SELECT → handler query param → OpenAPI → SDK → frontend → CSV).
- No data-plane collection change — the field was already collected; this ADR only adds two assignments in the existing `emit()` fan-out.
- Storage cost: VARCHAR + DEFAULT '' on two partitioned tables — negligible.
- The passthrough/translated badge (comparing `ingress_protocol` against the hit provider's adapter) is computed client-side in the management UI; this ADR provides the `ingress_protocol` column it needs.

## Alternatives considered

- **Keep telemetry-only (status quo).** Rejected: operators cannot answer protocol-mix questions from historical data; the dimension is already collected but invisible to the management UI.
- **Persist only to `request_logs`, not `trace_payloads`.** Rejected: `trace_payloads` is the direct data source for the request-list and session-timeline views; omitting the column there would force a join, violating ADR-0039's "summary dimension redundantly persisted" principle.
