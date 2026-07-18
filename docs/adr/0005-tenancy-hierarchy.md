# ADR-0005: Tenancy hierarchy — Tenant → Group → APIKey (three levels)

- Status: Accepted
- Date: 2026-06-30

## Context

The gateway is multi-tenant. The depth of the tenancy hierarchy is a root
decision: it shapes the data model, `plugin.Context`, the observability schema,
and the scope of every governance plugin (quota, rate limit, audit).

The existing code had implicitly assumed **two** levels: `plugin.Context` carries
only `Tenant` + `APIKeyID`, and the observability schema only `llm.tenant` +
`llm.api_key_id`. Reference gateways diverge: LiteLLM uses four levels
(Org→Team→User→Key) precisely because two proved insufficient once teams needed
separate budgets/usage; new-api uses two (User→Token).

A realistic near-term requirement was raised: one tenant (company) containing
several groups (teams), each needing its own usage view and budget. Two levels
cannot express this without later reshaping `Context`/schema and every plugin —
breaking orthogonality mid-project.

## Decision

Adopt **three levels: Tenant → Group → APIKey.**

- **Tenant** — top-level isolation boundary (e.g. a company/business unit).
- **Group** — a subdivision within a tenant (e.g. a team); owns its own
  budget/usage view and may scope model access.
- **APIKey** — the client credential, belongs to exactly one Group (and
  transitively one Tenant).

Budgets, quotas, and rate limits may be attached at any level; enforcement is
hierarchical (key → group → tenant), matching LiteLLM's multi-level model.

## Required changes (implemented in step 4, test-first)

- `plugin.Context`: add `Group` alongside `Tenant` and `APIKeyID`.
- Observability schema: add `llm.group` alongside `llm.tenant` / `llm.api_key_id`.
- Data model (step 7): `tenants`, `groups` (FK tenant), `api_keys` (FK group).

## Consequences

- Group-level usage/budget works out of the box; avoids the LiteLLM-style
  mid-life migration from two to more levels.
- Slightly more join/scope logic than two levels, accepted as the cost of not
  reshaping core contracts later.
- A fourth level (Org above Tenant) is intentionally NOT added; if ever needed,
  Tenant can be nested, but YAGNI for now.
