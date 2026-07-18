# ADR-0021: request_logs — data-plane per-request audit ledger

- Status: Accepted
- Date: 2026-07-02
- Builds on ADR-0014 (schema: denormalized append-only identity strings),
  ADR-0016 (fail-open async recording), ADR-0019 (read API patterns, deferred
  here), and `design/observability.md` (LLM semantic fields).

## Context

The data plane needs a durable, queryable record of every LLM request for
business audit and compliance: who called, which model/provider served the
request, token counts, latency, rejection/error type, and which plugin blocked
it if any.

This record is not the same thing as:

- `audit_logs`: management-plane mutation audit, keyed by operator/action/
  resource/before/after.
- `usage_records`: billing/reconciliation output, keyed around cost and token
  accounting.
- OTel traces/metrics/logs: operational telemetry, possibly sampled and exported
  to external backends.

Migration `00004_request_logs.sql` introduced `request_logs` as a range-
partitioned table with denormalized identity strings, but the design decision was
not captured by an ADR. This ADR records why it exists, how it is written, and
what phase 1 intentionally defers.

## Decision

### 1. Use a dedicated `request_logs` table, not `audit_logs`

`request_logs` is a data-plane request ledger. `audit_logs` remains the
management-plane mutation ledger.

They differ in schema and write path:

- `audit_logs` stores operator actions: `operator_id`, `action`,
  `resource_type`, `resource_id`, `before`, `after`, `tenant`, `created_at`.
- `request_logs` stores per-request facts: tenant/group/key identity,
  provider/model, stream flag, token counts, TTFT/duration, error type,
  blocking plugin, and fallback flag.
- `audit_logs` is written by the admin/RBAC mutation path.
- `request_logs` is emitted once from the data-plane handler's deferred telemetry
  collection point and written asynchronously.

Rejected: reuse `audit_logs` with `resource_type=request`. That would overload a
human-operator audit table with high-volume request traffic, make before/after
JSONB meaningless for data-plane requests, and couple two different retention and
query patterns.

### 2. Write 100% of request rows; do not sample

`request_logs` is a business/compliance ledger, not sampled operational
telemetry. Every data-plane request should attempt to append one row, including:

- successful non-streaming requests;
- successful streaming requests;
- upstream failures;
- auth/model-permission rejections;
- plugin rejections such as quota/rate-limit/content blocks.

The same accumulated request facts are fanned out to both OTel telemetry and the
request ledger, but these serve different purposes: OTel can be sampled and
exported; `request_logs` is local durable audit state.

The table must not store prompt/completion bodies or raw credentials. Those are
sensitive and large; if body-level audit is ever required, it belongs behind an
explicit audit plugin and retention policy, not in this core ledger.

### 3. Record asynchronously and fail open

The request path must never block on the request-log sink. The recorder uses a
bounded in-process channel and a worker goroutine:

- `Record` does a non-blocking enqueue.
- If the buffer is full, the row is dropped and a drop counter is incremented.
- If the sink INSERT fails, the row is dropped and the counter is incremented.
- The original user request continues unaffected.

This mirrors ADR-0016's principle for non-money audit/reconciliation data: quota
and billing settlement are the consistency-critical path; request audit is not
allowed to throttle paying traffic.

Accepted phase-1 trade-off: dropped request-log rows are counted in-process and
the first buffer-full drop logs a warning. A first-class Prometheus metric such as
`request_logs_dropped_total` is deferred.

Rejected: synchronous INSERT or backpressure on a full buffer. That would turn a
PostgreSQL/audit-table slowdown into user-visible data-plane latency or errors.

### 4. Store denormalized identity strings, not FKs

`request_logs` follows `usage_records`: tenant, group, and API key identities are
stored as strings (`tenant`, `group_name`, `api_key_id`) with no foreign keys.

Reasons:

- The data plane already carries these public identities from auth; it does not
  need surrogate DB IDs on the hot path.
- Append-only audit rows should preserve identity as it was at request time,
  independent of later tenant/group renames, API-key revocation, or deletes.
- Avoiding FKs keeps this high-write ledger independent from tenant lifecycle
  operations.

Rejected: FK to `tenants`/`groups`/`api_keys`. That would make historical rows
sensitive to later lifecycle changes and would require the data plane to carry or
look up surrogate IDs solely for audit insertion.

### 5. Partition by `created_at` from day one

Every LLM request attempts to append one row, so this is a high-write table.
`request_logs` is created as:

- `PARTITION BY RANGE (created_at)`;
- primary key `(id, created_at)`;
- `request_logs_default` default partition for phase 1;
- indexes on `(created_at)` and `(tenant, created_at)`.

This mirrors `usage_records`: partitioning is cheap before the table is large and
painful to retrofit later. Phase 1 uses the default partition so inserts work
without monthly partition operations. Phase 2 may add monthly partitions,
retention/TTL, and archival jobs.

Rejected: a single unpartitioned table. It is simpler initially but creates an
avoidable migration and backfill problem once request volume grows.

### 6. Phase 1 inserts one row at a time

The phase-1 sink is intentionally simple: the worker drains one `RequestLog` at a
time and the repository executes a single parameterized `INSERT` per row.

This is acceptable because the write is off the request path and the buffer
bounds memory. It also keeps failure semantics simple: each row is either written
or dropped.

Deferred: batch flushing. If request volume or INSERT overhead justifies it, the
worker can batch rows into multi-value INSERTs while preserving the same
fail-open semantics.

### 7. Read API (delivered)

Phase 1 shipped write-only. The read API is now delivered, following ADR-0019's
read patterns (mirroring `AuditQueryRepo`/`mountAudit` rather than `UsageQueryRepo`,
since tenant-scoping here is structural via the bound repo, not a super-admin
`tenant` query filter):

- `RequestLogQueryRepo` (`internal/store/requestlog_query.go`), tenant bound at
  construction — `""` = global (super-admin), non-empty = structurally scoped
  (tenant-admin);
- `GET /api/v1/request-logs` (`internal/admin/requestlog_handlers.go`), mounted
  for both roles without a role gate (scope resolved from the operator inside
  the handler, same seam as `/usage` and `/audit`);
- bounded `[from, to)` time ranges, keyset pagination on `(created_at, id)`,
  reusing the same cursor encoding as usage/audit;
- filters for `provider`, `model_requested`, `error_type`, `blocked_by`;
- no new migration — hits the existing `idx_request_logs_tenant_created` /
  `idx_request_logs_created_at` indexes;
- reads are not audited (ADR-0017 §5, same as usage/audit);
- `docs/openapi/admin.yaml` gained `RequestLogEntry`/`RequestLogList` schemas
  and the `/api/v1/request-logs` path; the TS admin client is regenerated
  (`sdk/typescript/src/admin-schema.d.ts`).

No Control Panel UI page yet — that remains a separate, later piece of work.

## Consequences

- `request_logs` is a separate data-plane ledger from `audit_logs` and
  `usage_records`.
- The data plane keeps a fail-open async recorder configured from
  `cmd/gateway`; `router.go` emits once per request through the shared telemetry
  accumulator.
- `RequestLogRepo.Record` is a single-row INSERT and does not bump
  `config_generation`; request logs are runtime ledger data, not config content.
- The ledger stores semantic request facts but not prompt/completion bodies or raw
  credentials.
- Phase-1 limitations remaining:
  - default partition only, no monthly partition management;
  - no Control Panel UI page (read API is delivered, §7);
  - no TTL or archival job;
  - no batch INSERT;
  - no exported `request_logs_dropped_total` metric.
- **Closed**: `model_resolved` and `fallback` are now populated. `Dispatcher.
  Forward`/`ForwardStream` (`internal/proxy/dispatcher.go`) return a
  `DispatchResult{Provider, ModelResolved, Fallback, RetryCount}` instead of a
  bare provider string; `telemetryAcc` (`internal/proxy/telemetry.go`) carries
  these through to both `RequestTelemetry` (OTel) and `RequestLog` (the
  ledger). `RetryCount` is OTel-only (not a ledger column — this ADR's schema
  does not carry it).
- **Closed**: keyset-paginated read API (§7) — `RequestLogQueryRepo` +
  `GET /api/v1/request-logs`.
- Phase-2 upgrade points remaining: monthly partitions, retention/archival,
  batch flush, dropped-row metric, Control Panel UI page.
