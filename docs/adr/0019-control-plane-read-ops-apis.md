# ADR-0019: Control-plane read/ops APIs — observability reads and atomic quota top-up

- Status: Accepted
- Date: 2026-07-01
- Builds on ADR-0013 (quota: direct-PG, int64 micro-units, TryDebit/Settle),
  ADR-0014 (schema: denormalized usage_records, append-only audit_logs),
  ADR-0016 (async usage recording), ADR-0017 (RBAC: operator auth,
  tenant-scoped-repository isolation, audit-on-mutation).

## Context

Step 7 built the management-plane **write** surface (config CRUD, tenant/key
management, snapshot) and the data plane **persists** rich operational data:
`usage_records` (every request: tenant/group/key, provider, model, tokens,
cost — month range-partitioned, indexed on `(tenant, created_at)`),
`audit_logs` (every config/identity mutation), and `quotas` (per-scope balance).

But that data is **write-only from the API's perspective**: `UsageRepo` and
`AuditRepo` have *zero* read methods, `QuotaRepo.Balance` is readable in Go but
has no endpoint, and there is no operator/group management beyond bootstrap. The
control panel needs to turn persisted data into readable, operable APIs.

Two design questions genuinely need deciding (the rest — groups CRUD, operator
CRUD, tenant delete — are mechanical applications of the ADR-0017 patterns and
belong in the PRD, not here):

1. **How reads respect tenant isolation, paginate, and stay cheap** on
   append-only/partitioned tables.
2. **How an admin quota top-up stays correct** against the data plane's
   concurrent hot-path debits.

## Decision

### 1. Reads reuse the ADR-0017 authorization model, with a query-repo per aggregate

Read endpoints follow the exact seam mutations use (ADR-0017): `authnMiddleware`
resolves the operator; `requireSuperAdmin` / `requireTenantAdmin` gates the
route; tenant-scoped reads go through a **repository bound to the operator's
tenant at construction** (`store.NewXxxRepo(db, tenantID)`), so a handler
*cannot* express a cross-tenant query. New read repos (`UsageQueryRepo`,
`AuditQueryRepo`) follow the same constructor-binds-tenant rule.

**Two visibility tiers, along the ADR-0014 tenancy line:**
- **super-admin** — global reads (any tenant's usage/audit/quota, all operators).
- **tenant-admin** — reads scoped to their own tenant only (structurally).

Config/identity **mutations remain audited; reads are NOT audited** (ADR-0017 §5
stands — audit noise). This means a super-admin reading another tenant's usage
leaves no audit trail; accepted for phase-1 (revisit if compliance requires
read-audit).

### 2. Read APIs are paginated, time-bounded, and index-aligned

- **Usage queries** MUST take a `[from, to)` time range and are served from
  `usage_records` using the existing `(tenant, created_at)` / `(created_at)`
  indexes and month-partition pruning. Tenant-scoped queries hit
  `idx_usage_records_tenant_created`; global (super-admin) queries are
  time-range-bounded to prune partitions. **Keyset (seek) pagination on
  `(created_at, id)`**, not OFFSET, so deep pages stay O(limit) on this
  high-write table. A hard default+max `limit`.
- **Usage aggregation** (sum tokens/cost grouped by day / model / provider /
  key) is a **SQL `GROUP BY` over the same time-bounded, tenant-filtered scan**,
  returned as summary rows. Phase-1 computes on read (no materialized rollups);
  a rollup table is a phase-2 optimization if the scan cost bites.
- **Audit queries** filter by `[from, to)` + optional `resource_type` / `action`
  / `operator_id`, newest-first, keyset-paginated. Tenant-admin reads are scoped
  by the new `tenant` column (decision 4); super-admin sees all. `audit_logs`
  gets an index on `(created_at)` (and, for tenant-admin, `(tenant, created_at)`)
  so reads don't full-scan; queries always time-bound. A migration adds the
  column + indexes.
- **Quota reads** expose `QuotaRepo.Balance` per scope (and a list of a tenant's
  scopes); trivially cheap (PK lookups).

### 3. Quota top-up is an atomic increment, never an absolute set

The existing `QuotaRepo.SetBalance` is `INSERT … ON CONFLICT DO UPDATE SET
balance = EXCLUDED.balance` — an **absolute overwrite**. Using it for admin
top-up would race the data plane: a concurrent `TryDebit`/`Settle` between the
admin's read and write would be silently clobbered (lost debit = free tokens, or
lost credit).

Admin quota adjustment is therefore an **atomic relative increment**, in the DB,
in one statement — the same consistency primitive as the hot path (ADR-0013):

```sql
INSERT INTO quotas (scope, balance, currency, updated_at)
VALUES (?, ?, ?, now())
ON CONFLICT (scope) DO UPDATE
  SET balance = quotas.balance + EXCLUDED.balance, updated_at = now()
```

- New `QuotaRepo.TopUp(ctx, scope, delta, currency)` performs `balance += delta`
  atomically; positive delta credits, negative debits (e.g. a correction). It
  **never reads-then-writes** in the app, so it cannot clobber a concurrent
  debit/settle.
- `SetBalance` (absolute) is retained **only** for initial provisioning of a
  brand-new scope and for tests; the admin *top-up/adjust* endpoint uses `TopUp`.
  This keeps "money mutation is a single atomic SQL statement" invariant from
  ADR-0013 intact across both planes.
- Quota mutations **are audited** (resource_type `quota`, resource_id = scope,
  after = delta) — they are identity/config-class changes under ADR-0017 §5.

### 4. Audit rows carry the AFFECTED tenant, so a tenant sees ops done *to* it

`audit_logs` gains a nullable `tenant` column. The audit middleware sets it to
the **tenant the mutation affects** (derived from the resource), NOT the
operator's own tenant — so a super-admin action *on* tenant X (top-up X's quota,
revoke X's key, create X's tenant-admin) is visible to X's tenant-admin:

- quota scope `tenant:X[...]` → `X`; api-key/group → the row's tenant; tenant
  create/delete → that tenant; **global config (provider/model/route/plugin) →
  NULL** (platform-level, no owning tenant, super-admin-only in audit reads).
- `AuditQueryRepo` bound to a tenant filters `WHERE tenant = ?`; super-admin
  (unscoped) sees all including NULL. Historical rows (pre-column) are NULL.
- This makes "affected tenant" the audit's tenancy key; deriving it lives in the
  audit middleware (a small per-resource-type resolver), keeping handlers clean.

### 5. OpenAPI is the single source of truth for the admin API contract

The admin REST surface is described by a checked-in **OpenAPI 3 spec**. It is the
contract both sides build against, enabling front-end/back-end separation:

- The TS **admin client is code-generated** from the spec (not hand-written), and
  is what both the future UI and the SDK/contract tests consume — types can't
  drift from the wire. (The existing OpenAI-compatible data-plane SDK stays as
  is; admin is a separate generated client / namespace.)
- Contract tests run the generated client against the running admin (on the e2e
  harness), so a spec/impl divergence fails tests.
- The spec doubles as UI-developer documentation and a mock source for
  front-end work before the back-end lands.
- Go handlers are validated against the spec in tests (request/response shape),
  keeping the spec authoritative rather than aspirational.

### 6. Uniform list envelope + configurable CORS (front-end-separation ready)

- **All list endpoints return `{ "data": [...], "next_cursor": "<opaque|empty>" }`**,
  including the existing config lists (providers/models/routes/plugins/tenants)
  — the bare-array responses are changed to the envelope now, before any UI
  consumes them, so pagination and shape are uniform. `next_cursor` empty means
  last page. Non-paginated lists still use the envelope (cursor always empty) for
  a single consistent client shape.
- **CORS** is a configurable admin middleware (allowed origins from config;
  empty = same-origin only, the safe default). Required because a
  front-end-separated UI is served from a different origin than the admin API.

## Consequences

- **New migration:** `audit_logs` gains a nullable `tenant` column + indexes on
  `(created_at)` and `(tenant, created_at)` (append-only btrees; queries always
  time-bound). The audit middleware backfills `tenant` going forward (historical
  rows stay NULL).
- **New read repos:** `UsageQueryRepo(db, tenantID)` and `AuditQueryRepo(db,
  tenantID)` (tenant-bound) plus super-admin (unscoped) variants or a `tenantID
  == 0 ⇒ all` convention — chosen so the tenant filter is still structural, not
  a forgettable clause. Query methods take `[from,to)` + keyset cursor + limit.
- **New QuotaRepo.TopUp** (atomic increment); `SetBalance` stays for provisioning.
- **Audit middleware** gains an affected-tenant resolver (per resource_type).
- **OpenAPI 3 spec** checked in; TS admin client generated from it; contract
  tests + Go request/response validation keep spec authoritative.
- **Uniform `{data,next_cursor}` list envelope** across all list endpoints
  (existing bare-array config lists migrated to it); **configurable CORS**
  middleware on the admin server.
- **Soft-delete**: tenant (and other) delete sets `enabled = false`, preserving
  usage/audit referential history (mirrors api_keys `revoked_at`).
- **New endpoints (detailed in the PRD):** usage list + aggregate, audit list,
  quota read + top-up, plus the mechanical CRUD gaps (groups, operators, tenant
  soft-delete). Super-admin gets global variants; tenant-admin gets tenant-scoped.
- **Reads are not audited**; **quota top-up is** audited.
- **Deferred (phase-2):** materialized usage rollups, read-audit for compliance,
  the front-end UI itself, cost/billing invoice generation.
- All of the above lands **test-first** against embedded-postgres (`make
  test-db`) + e2e on the harness: isolation tests (tenant-admin cannot read
  another tenant's usage/audit; but DOES see super-admin ops on its own tenant),
  keyset-pagination correctness, aggregation sums, generated-client contract
  tests, and — critically — a **concurrency test that a TopUp interleaved with
  TryDebit/Settle never loses an update** (the raison d'être of decision 3).
