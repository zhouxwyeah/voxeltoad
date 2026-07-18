# ADR-0014: Management-plane schema — hybrid row+JSONB config, flat-scope quota, global config / tenant-scoped keys

- Status: Accepted
- Date: 2026-06-30
- Builds on ADR-0005 (tenancy), ADR-0006 (key auth), ADR-0012/0013 (billing &
  quota), ADR-0007 (snapshot channel).
- Defers: provider credential storage (`provider_credentials` encryption vs
  `APIKeyRef = env://`) to its own ADR; migration tooling and snapshot-version
  derivation to ADR-0015 (next).

## Context

Step 7 introduces PostgreSQL. The schema is constrained by three forces that
pull in different directions:

1. **The snapshot must round-trip.** Config tables must serialize back into the
   exact `config.Dynamic` tree the data plane polls (`Provider`, `Model` with
   nested `Upstreams`/`Pricing`, `Route` with `RouteProvider`, `PluginConfig`
   with free-form `Params`). Three of four top-level types have nested arrays.
2. **The quota debit is on the hot path** and must be a single atomic SQL
   statement (ADR-0013 pre-debit/settle).
3. **Config structs still change** (e.g. `DefaultMaxTokens` landed in step 5),
   so a migration-per-field cost is real.

## Decision

### 1. Config tables: hybrid identity-columns + JSONB spec

Promote to real columns only what is **queried, constrained, or filtered** by the
admin plane; keep leaf detail as a `spec JSONB` blob that serializes verbatim
into the snapshot.

```
providers(  id, name UNIQUE, type, adapter, enabled, spec JSONB, created_at, updated_at )
models(     id, alias UNIQUE, enabled, spec JSONB, created_at, updated_at )
routes(     id, model_alias UNIQUE, strategy, enabled, spec JSONB, created_at, updated_at )
plugins(    id, name, phase, scope, enabled, spec JSONB, created_at, updated_at )
```

- `spec` holds the **entire** marshaled config struct (the whole
  `config.Provider`/`Model`/`Route`/`PluginConfig`). Snapshot serialization is
  then `SELECT spec` → unmarshal straight back into the struct — no hand-written
  merge, impossible to drift, and **adding a struct field needs no migration**.
  The identity columns (`name`/`alias`/`model_alias`, `enabled`, plugin
  `phase`/`scope`) are pure denormalization for `UNIQUE` constraints and admin
  queries.
- Real `UNIQUE` constraints on `name`/`alias`/`model_alias`; `enabled` for
  toggling without delete (mirrors `PluginConfig.Enabled`).
- **Cross-references are by name string** (route→provider, upstream→provider),
  matching the structs, and are **validated in the admin service layer at write
  time** (clear 400, not a PG constraint violation). PG is not the integrity
  authority for these references — the references aren't surrogate FKs.

Rejected: fully-normalized child tables (strong FK integrity, but a hand-written
serializer that must stay byte-compatible with the structs + a migration per
field — too costly for fast-moving config). Pure-JSONB documents (simplest
serialization, but loses the `UNIQUE`/`enabled`/identity columns the admin plane
queries on).

### 2. quota identity: flat scope string

```
quotas( scope VARCHAR PRIMARY KEY, balance BIGINT NOT NULL, currency, updated_at )
```

The data-plane debit is exactly `UPDATE quotas SET balance = balance - :est
WHERE scope = :s AND balance >= :est` — one indexed-equality atomic update, no
join, ideal for the hot path (ADR-0013). Scopes are **independent per-level
strings** (`tenant:a`, `group:b`, `key:c`) — not a single composite — matching
`billing.scopesOf`: a request debits *each* configured level (hierarchical
ceilings, the LiteLLM model), and each level is its own `quotas` row.

- **Referential integrity is enforced one layer up**: the admin service validates
  a scope against `tenants`/`groups`/`api_keys` at write time; a periodic /
  admin-triggered sweep flags orphans. The hot path stays a single statement.
- **absence = unlimited** (ADR-0012) survives: no row for a scope ⇒ `TryDebit`
  returns ok without touching a row ⇒ that level is unlimited. We do **not**
  pre-create rows per key. Fail-closed (ADR-0013) triggers only when the store
  *call* fails, never when a scope is simply absent.

Rejected: three nullable FK columns + CHECK (PG-authoritative integrity, but the
hot-path `TryDebit` needs a composite/join per request — pays latency for
integrity better placed at the write boundary).

### 3. Global config, tenant-scoped keys/quotas/usage

`providers` / `models` / `routes` / `plugins` are **platform-level** (no
`tenant_id`; only super-admin manages them; the snapshot is a single global
document). `tenants` / `groups` / `api_keys` / `quotas` / `usage_records` are
**tenant-scoped**. This is the LiteLLM org/team shape: the platform curates
upstreams, tenants consume them with their own keys and budgets.

Consequence for RBAC (#4): tenant-admins manage their own keys/groups/quotas and
read their usage; they cannot touch providers/models/routes. If a future product
needs tenant-supplied providers (BYO-key), this flips to per-tenant config and is
a new ADR — not built now.

### Tenancy + auth + audit tables (per ADR-0005/0006)

```
tenants(    id, name UNIQUE, enabled, created_at, updated_at )
groups(     id, tenant_id FK→tenants, name, enabled, created_at, updated_at,
            UNIQUE(tenant_id, name) )
api_keys(   id, key_id UNIQUE, hash CHAR(64) UNIQUE NOT NULL, tenant_id FK,
            group_id FK, expires_at TIMESTAMPTZ NULL, allowed_models JSONB,
            revoked_at TIMESTAMPTZ NULL, created_at )
usage_records( id, tenant, group_name, api_key_id (denormalized strings),
            provider, model, prompt_tokens, completion_tokens, cost BIGINT,
            created_at )
audit_logs( id, operator_id, action, resource_type, resource_id,
            before JSONB, after JSONB, created_at )
```

- `api_keys.hash` is SHA-256 hex, the data-plane lookup key (ADR-0006); plaintext
  never stored. **Soft-delete** (`revoked_at`) preserves revocation history and
  keeps the key_id resolvable for audit lookups; `allowed_models` is JSONB (read
  whole, never element-queried — matches `KeyRecord.AllowedModels`).
- `usage_records` is **append-only** with **denormalized string identities**
  (tenant/group names + public `api_key_id`): the data plane only ever has these
  (ADR-0006), and an audit/reconciliation row should preserve identity
  as-it-was, independent of later renames/deletes — so it carries **no FK** into
  `api_keys`/`tenants`. Indexed `created_at` and `(tenant, created_at)` for
  billing queries; `cost` is `BIGINT` micro-units (ADR-0013). **Month
  range-partitioning is adopted from day one** (cheap now, painful to retrofit on
  the highest-write table).
- `audit_logs` is append-only, excluded from the snapshot, and only populated
  once RBAC (#4) supplies `operator_id`.

### Money is BIGINT micro-units everywhere (ADR-0013)

`quotas.balance`, `usage_records.cost`, and pricing inside `models.spec` are
integer micro-units. `currency` is a label, no arithmetic.

## Consequences

- Snapshot serialization (ADR-0015) reads each `spec` JSONB → unmarshals to the
  struct — no struct-rebuild, no migration per config field.
- Cross-reference and scope integrity move to the **admin service layer**;
  step-7 admin handlers must validate provider/model/route name references and
  quota scopes against the tenancy tables, returning 400 on dangling refs.
- The `QuotaStore` PG implementation is one atomic `UPDATE` for `TryDebit` and
  one for `Settle` (ADR-0013); `quotas` needs only the PK on `scope`.
- RBAC enforcement boundaries follow the global-vs-tenant split (#4 / future
  ADR): providers/models/routes = super-admin; keys/groups/quotas/usage =
  tenant-scoped.
- Provider upstream-credential storage is still open (encryption vs `env://`
  ref); `providers.spec.api_key_ref` currently carries the ADR-0003 reference
  string — revisited in its own ADR before any secret is typed into PG.
- All of the above lands **test-first** against embedded-postgres (`make
  test-db`), one focused commit per cohesive unit.
