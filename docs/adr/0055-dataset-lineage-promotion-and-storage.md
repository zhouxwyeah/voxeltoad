# ADR-0055: Dataset lineage, payload promotion, and storage

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0039](0039-llm-trace-payload-capture.md) (trace payloads), [ADR-0021](0021-request-logs-data-plane-audit-ledger.md) (request ledger), [ADR-0054](0054-feedback-event-and-evaluation-boundary.md) (FeedbackEvent)

## Context

`trace_payloads` captures prompt and completion bodies for debugging, but it is
default-off, short-retention (7-day partitioned DROP), fail-open, and explicitly
not a long-term data store. Treating it as a training or evaluation dataset would
conflate debugging telemetry with governed data assets, break retention
expectations, and expose sensitive content to uncontrolled reuse.

Enterprise teams nonetheless need curated datasets for offline evaluation, model
comparison, regression testing, and future fine-tuning. These datasets must have
stable identity, versioning, ownership, provenance, access control, and
retention independent of the transient trace ledger.

Object storage is the natural home for large payloads in production, but
requiring it as a hard dependency would block single-binary deployments, local
development, and small installations. PostgreSQL is already the only required
stateful dependency and should remain sufficient for default and small-scale
deployments.

## Decision

### Dataset, DatasetVersion, and DatasetItem are governed entities

- **Dataset** — a named, tenant-scoped collection with an owning Application or
  Group, a data classification, and a retention policy.
- **DatasetVersion** — an immutable snapshot of a dataset's item manifest at a
  point in time. Edits produce a new version; versions are never mutated in place.
- **DatasetItem** — one example within a version, carrying provenance references
  to `request_logs`, `trace_payloads`, and/or `FeedbackEvent`, plus a
  `PayloadRef` pointing to stored content.

Promoted content is a governed copy, not a live view into `trace_payloads`. Once
promoted, a DatasetItem has its own retention, deletion, and audit lifecycle
independent of the short-lived trace ledger.

### Payload promotion requires authorization, redaction, and audit

A trace payload becomes a DatasetItem only when all of the following hold:

1. **Tenant policy opt-in** — the tenant has enabled dataset promotion; tenants
   that have not opted in cannot promote content.
2. **Scoped permission** — the operator has a dedicated dataset-promotion
   permission, not merely read access to traces.
3. **Redaction check** — content passes the configured PII/sensitive-data
   redaction policy, or is explicitly marked as exempt with a recorded reason.
4. **Mutation audit** — the promotion action, its actor, its inputs, and the
   resulting DatasetItem are recorded in the audit log.
5. **Read/delete audit** — subsequent reads and deletions of promoted items are
   audited.

Promotion is a management-plane operation. It must not run on the data-plane
request hot path.

### PostgreSQL is the default payload backend; object storage is optional

- **`database` backend** — DatasetItem payloads are stored in PostgreSQL. This is
  the default and is suitable for testing, local development, and small-scale
  production.
- **`object` backend** — DatasetItem payloads are stored in a configurable object
  store (e.g. S3-compatible). PostgreSQL stores only the `PayloadRef` manifest,
  permissions, and lineage. This is recommended for production scale.

Object storage connection is configured via static bootstrap configuration, not
the hot-reloadable `gateway_settings`, because it is infrastructure-level state.

### Missing object storage does not break the gateway

If object storage is not configured:

- the data plane (proxy, billing, request logging, feedback ingestion) continues
  to operate normally;
- metadata-only Dataset, DatasetVersion, and FeedbackEvent ledgers continue to
  function;
- payload promotion that requires object storage returns an explicit
  configuration error rather than silently falling back to unbounded PostgreSQL
  growth;
- the `database` backend remains available for deployments that explicitly choose
  it.

Object storage is not a startup dependency of the gateway process.

### Lineage is referential and non-destructive

DatasetItem provenance references original `request_logs`/`trace_payloads`/
`FeedbackEvent` by identifier. If the original trace payload is purged by
retention, the promoted DatasetItem and its payload copy survive because they are
independent governed assets. Lineage records the origin, not a live dependency.

## Consequences

- Enterprises can build governed evaluation and regression datasets from real
  traffic without treating the trace ledger as a permanent store.
- Promoted content has independent retention and audit, so trace cleanup does not
  destroy curated assets and curated assets do not extend trace retention.
- Default PostgreSQL storage keeps single-binary deployment viable; production
  object storage scales without changing the data model.
- Promotion is deliberately a managed, audited, permission-gated operation, not
  an automatic pipeline, to control sensitive-data exposure.
- Future work may add automated promotion rules, but they must still satisfy the
  same authorization, redaction, and audit requirements.

## Alternatives considered

### Promote by keeping trace_payloads indefinitely

Rejected because it breaks the short-retention, default-off, debugging-only
contract of ADR-0039 and exposes sensitive content to uncontrolled long-term
reuse.

### Store all dataset content only in an external warehouse

Rejected because it loses unified governance, lineage, permissions, and deletion
control inside the gateway. The gateway would export events but could not answer
"which requests contributed to this dataset version" or enforce deletion.

### Require object storage as a hard dependency

Rejected because it blocks single-binary deployment, local development, and
small-scale production. PostgreSQL remains the only required stateful dependency.

### Automatic promotion for all trace payloads

Rejected for this phase. It would expand sensitive-data exposure without
explicit per-tenant authorization and redaction control. Automatic promotion
rules may be added later but must still satisfy the same gate.
