# ADR-0051: Application as a first-class governance identity

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0005](0005-tenancy-hierarchy.md) (Tenant → Group → APIKey), [ADR-0006](0006-apikey-auth-and-data-channel.md) (API key identity), [ADR-0033](0033-data-plane-keys-not-bound-to-roles.md) (data-plane authorization boundary)

## Context

The existing governance hierarchy attributes every data-plane request to a Tenant,
Group, and APIKey. That answers who owns the credential and which organizational
budget is charged, but it does not provide a stable identity for the business
system consuming AI. API keys rotate, one application may use multiple keys, and
a centrally owned application may be consumed by several groups.

Using a caller-supplied application header would make cost attribution and policy
enforcement forgeable. Treating each environment or agent run as a separate
application would instead make the catalog unstable and conflate long-lived
business identity with runtime execution context.

Application-level usage, budgets, routing policy, and model access therefore need
a stable identity independent from both organizational ownership and credentials.

## Decision

### Application is a first-class, tenant-scoped governance entity

An **Application** represents a long-lived business system or product that consumes
AI. It is the primary workload identity for application-level attribution and
policy. It is not an API credential and is not a transient agent run.

Each Application:

- belongs to exactly one Tenant;
- has exactly one owning Group (`owner_group_id`) for maintenance responsibility;
- may be consumed by API keys belonging to multiple Groups in the same Tenant;
- may have multiple API keys for rotation and environment isolation;
- has an enabled/disabled lifecycle independent from individual keys;
- retains its identity in historical usage and request ledgers after disablement.

Application and Group are orthogonal attribution axes:

- Application answers **what business system produced the demand**;
- the API key's Group answers **which organizational consumer produced the spend**;
- `owner_group_id` answers **which group maintains the application**.

This does not add a fourth level to ADR-0005's tenancy hierarchy. Tenant remains
the isolation boundary, Group remains the organizational governance level, and
Application adds a tenant-scoped workload axis.

### Application identity is derived from trusted credentials

An API key may bind to at most one Application. The authenticated key record, not
a caller-controlled request header, supplies `application_id` to the data-plane
context. One Application may bind many keys; one key cannot represent multiple
Applications.

Existing keys may remain unbound during migration. Requests authenticated by an
unbound key are recorded as unattributed and cannot receive application-scoped
policy or budget. The target state is that every newly issued production key
binds to exactly one Application; unbound keys are a measurable migration debt to
be scanned and attributed, not a supported long-term mode.

A future JWT or workload-identity mechanism must resolve Application through a
trusted server-side mapping. It must not change the rule that caller-supplied
metadata alone cannot establish a governance identity.

### Environment is controlled credential context, not a deployment entity

Application denotes the logical system across `dev`, `staging`, and `prod`.
Environment is a controlled attribute of the bound credential and is snapshotted
into request/usage records. Application policy may later define per-environment
overrides while inheriting application defaults.

No `ApplicationDeployment` entity is introduced in this phase. A deployment
entity requires a separate decision when self-hosted inference or an independent
deployment lifecycle creates a real need.

### Agent, Run, and Workload remain distinct

This decision intentionally does not make Agent a first-class identity:

- **Agent** may later become an Application subtype, an Application-owned profile,
  or a delegated Principal; that choice depends on concrete Harness requirements.
  Neither the current User-Agent-inferred `agent_type` observation tag nor a future
  controlled Agent label is an independent authorization principal in this phase.
- **Run / Session** is a transient execution and accounting boundary.
- **Workload profile** is per-request routing context such as latency objective,
  quality floor, modality, and estimated token shape; it is not an identity.
- caller-supplied feature, customer, run, and session metadata may support
  attribution, but cannot directly grant access or control a budget.

Agent identity, run-level controls, budget semantics, and workload-aware routing
require follow-up decisions rather than being folded into Application.

## Consequences

- API key rotation no longer fragments application-level cost and usage history.
- A shared enterprise application can be owned centrally while preserving the
  consuming Group on every request.
- Usage and request schemas will need `application_id` and `environment` snapshots;
  query/report APIs will need the same dimensions.
- The management plane will need Application lifecycle APIs and key-binding flows.
- Application disablement can uniformly stop all bound credentials without
  revoking each key independently.
- Trusted credential binding makes application budgets and model policy enforceable.
- Unattributed legacy traffic remains visible as migration debt instead of being
  silently assigned from an untrusted header.
- Application-level budget, policy inheritance, routing, and retention semantics
  are deliberately left to follow-up ADRs.

## Alternatives considered

### Application as a free-form API key label

Rejected as the target model. It is a useful migration bridge but cannot preserve
identity across key rotation, enforce referential integrity, own lifecycle state,
or safely carry policy.

### Strict Group → Application → APIKey hierarchy

Rejected because centrally maintained applications are commonly consumed by
multiple organizational groups. It would force duplicate Application records or
special shared-key handling and would conflate ownership with consumption.

### Application belongs only to Tenant, without an owning Group

Rejected because it weakens operational responsibility and default management
scope. Cross-group consumption is supported without removing explicit ownership.

### Caller-supplied Application header

Rejected for governance identity. It is forgeable and therefore unsuitable for
budget, authorization, or trustworthy cost attribution.

### One Application per environment

Rejected because it duplicates the logical catalog and makes cross-environment
analysis unnecessarily difficult. Environment remains a controlled credential
attribute until a real deployment lifecycle is required.

### Generic Principal abstraction for Human, Service, Application, and Agent

Deferred. It would prematurely merge the existing management-plane Operator,
data-plane APIKey, workload identity, and future delegated Agent identity into one
large authorization redesign before concrete delegation requirements exist.
