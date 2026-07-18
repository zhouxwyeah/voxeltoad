# ADR-0015: Migrations & snapshot versioning — goose embedded, startup auto-migrate, config_generation counter

- Status: Accepted
- Date: 2026-06-30
- Builds on ADR-0014 (schema), ADR-0007 (snapshot channel / internal trust),
  ADR-0013 (quota). Bootstrap of the first admin operator is deferred to the
  RBAC ADR (#4).

## Context

Step 7 introduces PostgreSQL, so the schema needs a migration mechanism, the
snapshot endpoint needs a real version (it currently returns a hardcoded `"v0"`
at `internal/admin/server.go`), and first-boot bootstrap must be defined.

A standing project rule (recorded as feedback): **integration tests must hit a
real DB, not mocks, because a prior mock/prod divergence masked a broken
migration.** The corollary that governs this ADR: whatever creates the schema in
the `dbtest` harness MUST be the exact same artifact that creates it in
production. If `TestMain` ran `gorm.AutoMigrate` while prod ran hand-written DDL
(or vice versa), we would rebuild the very divergence the rule exists to prevent.

The snapshot version is correctness-critical, not cosmetic: the data-plane poller
does a conditional GET (`If-None-Match` → 304). A stale-but-equal version makes
the data plane silently run stale config forever.

## Decision

### 1. goose with `//go:embed`'d migrations; one canonical path for test and prod

Schema evolves through **ordered, versioned migrations applied by goose**, with
the migration files `//go:embed`'d into the binary. Migrations live in
`internal/store/migrations/` and are applied by a single `store.Migrate(db)`
function. **`TestMain` (dbtest) and the admin process call the identical
`store.Migrate`** — one artifact, zero divergence, satisfying the test-matches-
prod rule by construction.

Rejected:

- **GORM AutoMigrate** — never drops/renames columns, no down-migrations, no
  ordered auditable history. For a system holding money balances, "the schema is
  whatever the structs were at deploy time" is unacceptable, and it violates the
  test-matches-prod rule the moment any hand DDL is needed (which it will be,
  e.g. partitioning).
- **golang-migrate** — clean and mature, but SQL-only. ADR-0014 adopted
  month-partitioned `usage_records` and a JSONB `spec`; partition creation and
  future field-promotion backfills are *data/logic* migrations. goose runs those
  as **Go functions in the same ordered sequence**; golang-migrate would need a
  separate bolt-on mechanism.
- **Atlas** — most powerful (declarative diffing), but the heaviest (engine/CLI).
  More machinery than a phase-1 VM gateway with a hand-curated schema needs;
  consistent with prior lighter-dependency choices (removed etcd, interface-ized
  Redis). Remains a future option if schema sprawl justifies declarative diffing.

goose tracks schema shape in its own `goose_db_version` table.

### 2. Auto-migrate on admin startup, guarded by a PG advisory lock

The admin process runs `store.Migrate` on startup (before serving), wrapped in a
**PostgreSQL advisory lock** so concurrent admin instances don't race the
migration. A `migrate` subcommand is also exposed for ops/manual control. Fits
the phase-1 single-admin VM deployment; the advisory lock keeps it safe if more
than one admin instance ever starts together.

### 3. Snapshot version = a single `config_generation` counter

A single-row `config_generation(version BIGINT)` table is **incremented in the
same transaction as any config-table write** (providers/models/routes/plugins).
The snapshot endpoint reads it and returns it as the `ETag`; `If-None-Match`
match → 304. Monotonic, transactionally correct, cheap to read, and immune to the
stale-but-equal collision that breaks conditional GET.

Rejected: `max(updated_at)` (clock-skew fragile; a restore with old timestamps
looks unchanged; racy across tables). Content hash (self-validating but recomputes
on every poll, and identical-content writes don't bump it).

**Boundary:** the snapshot version (`config_generation`, config *content*) is a
different lifecycle from the schema migration version (`goose_db_version`, schema
*shape*). They are never conflated.

### 4. Bootstrap: internal token from env/config; operator seeding deferred; migrations carry no secrets

- **`X-Internal-Token`** (ADR-0007) is resolved via `config.ResolveSecret` from
  **bootstrap config / env, not the DB** — unchanged. It is not a migration
  concern.
- **First super-admin operator** must exist before any authenticated admin call.
  The seeding mechanism (explicit `bootstrap` subcommand vs env-on-first-start)
  is **deferred to the RBAC ADR (#4)**; the leaning is an explicit subcommand
  (seed migrations with credentials, or unrotated default admin/admin, are
  anti-patterns).
- **First tenant/group** is ordinary CRUD by the super-admin once they exist —
  no bootstrap needed.
- **Migrations carry no secrets** — only structural seed rows (e.g. the initial
  `config_generation` row at version 0).

## Consequences

- Add `goose` as a dependency; create `internal/store/migrations/` with the
  initial schema (ADR-0014 tables) as the first migration, plus the
  `config_generation` seed row.
- `store.Migrate(db)` is the single entry point; the `dbtest` `TestMain` calls it
  before `m.Run()`, replacing the bare smoke test — every repository test then
  runs against the real, migrated schema.
- The admin snapshot handler reads `config_generation` for its ETag; every
  config-mutating admin handler bumps it within its write transaction.
- `goose_db_version` and `config_generation` coexist with distinct meanings.
- Bootstrap-operator mechanism is an open item carried into the RBAC discussion.
- All migrations and the version wiring land **test-first** against
  embedded-postgres (`make test-db`), one focused commit per cohesive unit.
