# ADR-0017: Management-plane RBAC — operator auth, scoped-repository isolation, bootstrap, audit

- Status: Accepted (Phase-2 custom roles implemented 2026-07-08)
- Date: 2026-06-30
- Builds on ADR-0014 (schema: global config / tenant-scoped keys; `operators`/
  `audit_logs` tables), ADR-0006 (client key auth — a *separate* system),
  ADR-0015 (migrations carry no secrets; bootstrap deferred here).
- Resolves the bootstrap-operator item deferred from ADR-0015/ADR-0013 grills.

> **Phase-2 实现**（2026-07-08）：`roles` / `role_permissions` 表 + `operators.role_id` FK 已落地（迁移 00011/00012），permission catalog 定义在 `internal/authz/permission.go`（28 个 `resource.action` 键 + `*` 通配），`requirePermission(perm)` + `requireTenantScoped()` 中间件已就绪，`requireSuperAdmin()` / `requireTenantAdmin()` 内部已切换为权限检查。session lookup 自动加载 role 的 permission set。两内置角色（super-admin `*` / tenant-admin 租户权限集）由迁移 seed。详见 `design/database.md` RBAC 边界节和 `docs/glossary.md` RBAC 节。

## Context

Step 7 exposes the admin REST surface (`/api/v1/{providers,models,routes,
api-keys,quotas,plugins}`). It needs human-operator authentication and
authorization. Two traps to avoid: (1) building fine-grained roles before any UI
or multi-operator demand exists (premature), while the RBAC *data model* is
painful to retrofit; (2) enforcing tenant isolation by "remembering" to add
`WHERE tenant_id = ?` per handler — which leaks the first time someone forgets.

Operator auth is a **distinct system** from client API-key auth (ADR-0006,
hashed keys, data plane). They must not be conflated.

The authorization boundary is already set by ADR-0014's tenancy split:
**global resources** (providers/models/routes/plugins) vs **tenant-scoped**
(tenants/groups/api_keys/quotas/usage). RBAC enforces along that line.

## Decision

### 1. Model full RBAC; enforce two roles now (super-admin, tenant-admin)

Ship the `operators` + `roles` data model capable of full RBAC, but **enforce
only two roles** in step 7:

- **super-admin** — global: manages tenants and platform-level config
  (providers/models/routes/plugins).
- **tenant-admin** — bound to one tenant: manages its groups/api_keys/quotas,
  reads its usage; **cannot** touch global config.

A read-only `viewer/billing` role is cheap to add later via the same tables and
is **not** implemented now. The resource's tenancy (ADR-0014) *is* the
authorization boundary: global → super-admin only; tenant-scoped → tenant-admin
within their own tenant.

### 2. Operator auth: local accounts (argon2id) + server-side sessions

Operators authenticate with **email + password hashed using argon2id**; a
successful login issues an **opaque server-side session token** stored in a
`sessions` table (revocable instantly — firing an operator kills their sessions).
Self-contained, no external dependency, correct for phase-1 VM.

Rejected for now: stateless JWT (needs a denylist for instant revocation —
extra mechanism for no phase-1 benefit). Deferred: **OIDC/SAML SSO** as a
documented phase-2 behind an auth-provider seam; the `operators` table stays the
same, OIDC later adds an external-identity column. Login lockout/rate-limiting on
failed attempts is included.

This is wholly separate from client API-key auth (ADR-0006); the two share no
code path.

### 3. Tenant isolation: mandatory scoped-repository wrapper

Tenant-scoped repositories are **constructed from an authorization context that
carries the operator's tenant**; the repo injects `tenant_id` into every query
and exposes **no API to query without it**. A handler physically cannot
construct an unscoped query against tenant data. super-admin uses the unscoped
repos for global resources.

Chosen over PG Row-Level Security because it is **testable in pure Go** (fits the
test-first rule), has **no connection-pool footguns** (RLS needs per-tx
`SET LOCAL app.tenant_id` + reset-on-return, plus a super-admin bypass role), and
makes "the handler cannot bypass the tenant filter" a structural guarantee in the
service layer. RLS is strictly stronger (defends even a SQL-injection-bypassed
handler) and remains a **phase-2 defense-in-depth** option layered underneath the
wrapper.

### 4. Bootstrap: explicit idempotent `bootstrap` subcommand

The first super-admin is created by an explicit `voxeltoad-admin bootstrap --email …
--password …` subcommand that creates one super-admin **iff no super-admin
exists** (idempotent no-op otherwise). No credentials in version control, no
fixed default account, consistent with ADR-0015 ("migrations carry no secrets").

Rejected: env-on-first-start (`ADMIN_BOOTSTRAP_*`) — convenient but invites the
unrotated default-admin trap and puts credentials in the process environment.
Seed migration — anti-pattern (creds in VCS / fixed default).

The first **tenant/group** is then ordinary CRUD by the super-admin; the
`X-Internal-Token` (ADR-0007) remains env/config-sourced, not in the DB.

### 5. Audit logging: structural, on all mutations

Every config/identity **mutation** (create/update/delete on provider, model,
route, plugin, tenant, group, api_key, quota) writes an append-only `audit_logs`
row: `operator_id, action, resource_type, resource_id, before/after JSONB,
created_at`. Wired as a **thin wrapper/middleware in the admin service layer**
around mutating handlers — it cannot be forgotten per-handler (same structural
principle as §3). **Reads are not audited** (too noisy; revisit if compliance
requires it). `audit_logs` is append-only and excluded from the config snapshot.

## Consequences

- New tables/columns (migrations, ADR-0015): `operators` (email, argon2id hash,
  role, tenant_id NULL for super-admin), `roles` (or a role enum on operators if
  the model stays small), `sessions` (opaque token, operator_id, expires_at).
  `audit_logs` already exists (ADR-0014).
- Admin handlers split into an authn middleware (resolve session → operator) and
  an authz layer (role + tenancy check) that hands the handler a **scoped or
  unscoped repository** accordingly; tenant-scoped repos enforce `tenant_id`.
- A `bootstrap` subcommand is added to `cmd/admin` (or `voxeltoad-admin`); idempotent.
- OIDC/SSO and the `viewer` role and PG RLS are explicit phase-2 items, not built
  now.
- All of the above lands **test-first** against embedded-postgres (`make
  test-db`): isolation tests assert a tenant-admin cannot read another tenant's
  rows; authz tests assert tenant-admin is rejected on global config; bootstrap
  idempotency is tested. One focused commit per cohesive unit.
