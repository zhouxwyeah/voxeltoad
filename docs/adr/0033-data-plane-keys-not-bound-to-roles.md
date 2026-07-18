# ADR-0033: Data-plane API keys are a separate permission domain from management-plane roles

- Status: Accepted
- Date: 2026-07-09
- Builds on ADR-0017 (management-plane RBAC: `roles` / `role_permissions`,
  `operators.role_id`, permission catalog in `internal/authz/permission.go`),
  ADR-0006 (client API-key auth — a *separate* system from operator auth),
  ADR-0014 (tenancy hierarchy: Tenant → Group → APIKey).

## Context

Phase-2 RBAC (ADR-0017) shipped a full role model for **human operators**: a
`roles` table, a `role_permissions` join, an `operators.role_id` FK, and a
permission catalog of `resource.action` keys (`provider.write`, `usage.read`, …)
enforced by `requirePermission(perm)` in the admin service layer.

Immediately after, a proposal arose to **bind data-plane API keys to those
management-plane roles** — adding `api_keys.role_id`, loading the role's
permissions onto `KeyRecord` / `plugin.Context`, and authorizing data-plane
requests by `hasPermission`. This was implemented (migration 00014 + auth/path
plumbing) and then **deliberately reverted** (commit `4ea2ae8`): the binding was
a category error. This ADR records that decision so it is not re-litigated.

The error was conflating two orthogonal permission domains:

| Dimension | Management-plane Role (`roles`) | Data-plane API key (`api_keys`) |
|---|---|---|
| Holder | human operator (logs into the control panel) | client/app calling the gateway |
| Permission semantics | `resource.action` over *admin operations* (`provider.write`, `usage.read`, `role.write`, …) | *which models / how much rate / which tenant boundary* |
| Existing mechanism | `internal/authz/permission.go` catalog + `requirePermission` middleware | `allowed_models` + `group` rate/quota + `tenant_id` isolation |
| Code path | admin service layer (gin handler + `rbac` middleware) | data-plane proxy (`Authenticator` → `KeyStore.LookupByHash`) |

ADR-0006 already establishes that client API-key auth is a system **distinct**
from operator auth. The two share no code path and must not be coupled through a
shared permission model. A role such as `usage.read` has no meaningful
translation onto a key that is already scoped to a single tenant's models and
rate budget; forcing the mapping only muddies both models.

## Decision

**Data-plane API keys are NOT bound to management-plane roles.** A key's access
control is expressed entirely by the data-plane's native dimensions:

- `allowed_models` — which models the key may invoke;
- `group` — the rate limit and quota the key draws from;
- `tenant_id` — the tenant isolation boundary the key lives within.

Management-plane roles govern **only** what an operator may do inside the
control panel. They must not leak onto downstream data-plane credentials.

**Forward constraint (capability granularity).** If, in future, the data plane
needs finer *capability* control — e.g. allow/deny `streaming`,
`function_calling`, or a model family — that MUST be designed as a **capability
dimension carried on the API key itself**, independent of roles. It MUST NOT
reuse the `internal/authz/permission.go` catalog or the role model. The role
catalog stays management-plane-only by construction.

## Consequences

- The two systems remain code-path-disjoint: role checks live in the admin
  service layer; key checks live in the data-plane proxy. No shared permission
  catalog.
- `internal/authz/permission.go` (and any `role.read`/`role.write` semantics)
  is reserved for the control panel; it is never consulted on the data path.
- `api_keys` carries no `role_id` column; `KeyRecord`/`plugin.Context` carry no
  role-derived permission set.
- Future data-plane capability work (streaming/function_calling gating) starts
  from a new, key-scoped design — not from extending RBAC.
- See `docs/glossary.md` (RBAC & operators) for the terminology split; this ADR
  is the authority for "keys ≠ roles".
