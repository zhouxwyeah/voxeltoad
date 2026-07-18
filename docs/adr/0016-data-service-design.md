# ADR-0016: Data-service design — repo ownership, multi-scope debit transaction, async usage recording

- Status: Accepted
- Date: 2026-06-30
- Builds on ADR-0013 (quota pre-debit/settle, fail-closed), ADR-0014 (schema),
  ADR-0012 (billing/quota separation), `design/architecture.md` (layering).

## Context

Step 7's PG layer is consumed by two callers with opposite profiles: the admin
plane does ergonomic CRUD over many tables; the data plane does one money-
critical atomic operation per request. ADR-0013 put the data plane directly on
PG for quota; this ADR settles how the repositories are owned and accessed, how
the multi-scope debit stays atomic, and how usage records are persisted without
endangering either the request path or the balance.

Layering check (`design/architecture.md`): `store/` is L2; the dependency arrow
is `proxy|admin → …|store → …`, so **both planes may import `store`**. Rule 1
forbids `proxy ↔ admin` mutual import only — communication stays via the
snapshot. So "data plane imports the quota repository in `store`" is
layering-legal and does not couple the two planes.

Current billing wiring (`internal/billing/plugin.go`): `scopesOf` emits
**independent per-level scopes** (`tenant:X`, `group:Y`, `key:Z`) and the plugin
debits each — hierarchical ceilings (LiteLLM model). Pre currently calls
`Exceeded`, Post calls `Debit`; ADR-0013 replaces these with `TryDebit`/`Settle`.

## Decision

### 1. Repository ownership: shared `store` package, two access styles

Both planes import `internal/store`; `proxy` never imports `admin` (unchanged).
Within `store`:

- **Admin CRUD uses gorm** — ergonomic, matches the many-table management
  surface (ADR-0014 hybrid rows + JSONB).
- **The data-plane quota hot path uses raw SQL** (`pgx`/`database/sql` prepared
  `Exec`), not gorm. `TryDebit`/`Settle` are single atomic `UPDATE`s on the
  request path; routing money through gorm's reflection/session machinery is
  needless overhead. The cost — two DB access styles in one package — is
  accepted and contained to `store` (admin repos vs the quota repo).

The `QuotaStore` PG implementation lives in `store` and is injected into the
data-plane billing plugin; the admin repos live alongside it.

### 2. Multi-scope debit is one PG transaction, all-or-nothing

Pre-debit across the caller's scopes (`tenant:`/`group:`/`key:`) runs in **a
single PG transaction**: each scope's conditional
`UPDATE quotas SET balance = balance - :est WHERE scope = :s AND balance >= :est`
executes in the tx; **any zero-rows-affected ⇒ rollback the whole tx ⇒ reject**
(HTTP 402). This makes the multi-scope reservation exact — no scope is charged
unless all pass — with no hand-rolled compensation. One transaction per request
(N conditional updates) is the accepted hot-path cost; it is the reason the data
plane is on PG directly (ADR-0013).

`Settle` (Post) likewise reconciles all scopes' deltas; it is unconditional
(reconciles a reservation already made), **always runs**, and may share one tx
for the scope set. On total failure / no usage `actual = 0`, so Post issues a
**full refund** (`delta = est`) — never a no-op, which would leak the Pre
reservation (this changes the current `bill` early-return on nil usage, which was
correct only under the old check-then-debit model). The Pre estimate is a **completion-only ceiling** — `effectiveMaxTokens × max
completion rate across the alias's candidate providers` (no prompt term: the hit
provider is unknown before dispatch and there is no tokenizer; ADR-0013) — and
Post settles at the actual hit provider's rate against exact usage (prompt
included).

Rejected: sequential `TryDebit` + compensating `Settle` on first insufficient —
no transaction, but hand-rolled refunds that leak if the process crashes between
debit and refund. The single-transaction approach is exact by construction.

`TryDebit`/`Settle` therefore operate over **the scope set**, not one scope, so
the transaction boundary is owned by the store (illustrative):

```go
// TryDebit conditionally debits est from every scope in one tx. ok=false (no
// error) ⇒ at least one scope insufficient ⇒ nothing debited ⇒ reject.
TryDebit(ctx context.Context, scopes []string, est int64) (ok bool, err error)
// Settle reconciles all scopes by delta = est - actual (unconditional).
Settle(ctx context.Context, scopes []string, delta int64) error
```

Unconfigured scopes (no row) are skipped inside the tx (absence = unlimited,
ADR-0014), not treated as failures.

### 3. Async usage recording is fail-open: drop + metric

Usage records (`UsageRecorder`) are audit/reconciliation data, **separate from
the quota path** — the money already moved synchronously via `Settle`. So
recording **fails open** (opposite of quota's fail-closed, ADR-0013): a bounded
in-process buffer (channel) batch-flushes to PG; when the buffer fills (PG
down/slow), the record is **dropped and a `usage_records_dropped` metric is
incremented** — the request path is **never** blocked. ADR-0012 already sanctions
this under-billing-on-crash loss; keeping it bounded and observable is the phase-1
choice.

Rejected: block/backpressure (an audit-table outage would throttle paying
traffic — wrong for audit data). Deferred: spill-to-local-WAL with replay
(zero-loss, but adds a disk mechanism + replay logic) — a documented **phase-2
upgrade** if reconciliation strictness ever demands it.

### 4. Quota Settle and usage Record never share a transaction (explicit)

In Post: `Settle` runs **synchronously** against PG (consistent money path);
`Record` only **enqueues** to the buffer (the PG write is async/batched). They
are on different consistency paths (ADR-0012) and MUST NOT share a transaction —
a dropped or lagging usage record never touches, blocks, or rolls back a balance.

## Consequences

- `store` gains a raw-SQL quota repo (`TryDebit`/`Settle` over a scope set, one
  tx) plus gorm-based admin repos; the `QuotaStore` interface (ADR-0013) is
  reshaped to take `[]string` scopes so the transaction boundary lives in the
  store, and the billing plugin's Pre/Post call it with `scopesOf(c)`.
- The billing plugin changes from `Exceeded`/`Debit` (per-scope, check-then-act)
  to `TryDebit`/`Settle` (scope-set, transactional); Pre rejects on `ok=false`,
  Post **always** settles the est−actual delta (full refund when actual=0). All
  `float64` money becomes `int64` micro-units (ADR-0013). The plugin's nil-usage
  early-return is replaced by a full-refund settle. Lands test-first.
- `plugin.Context` gains a **rejection-status field** so the router can emit
  **402** (insufficient quota) vs **503/502** (quota store unreachable,
  fail-closed) vs the rate limiter's **429** — the single `Stop`→429 mapping is
  insufficient (ADR-0013). Small contract addition, test-first.
- A usage-recording worker (buffer + batch flush + drop metric) is added; the
  recorder's `Record` is a non-blocking enqueue.
- `design/architecture.md` line ~96 is updated: the data plane is **not** purely
  stateless — it polls the snapshot **and** holds a direct quota-store
  connection; PostgreSQL is the only stateful dependency for **both** planes
  (not just the management plane).
- Everything lands against embedded-postgres (`make test-db`), test-first, one
  focused commit per cohesive unit.
