# ADR-0056: Harness, Run, and tool-audit boundary

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0005](0005-tenancy-hierarchy.md) (tenancy), [ADR-0033](0033-data-plane-keys-not-bound-to-roles.md) (data-plane authorization), [ADR-0051](0051-application-governance-identity.md) (Application), [ADR-0052](0052-enterprise-budgets-and-resource-accounting.md) (budgets), [ADR-0054](0054-feedback-event-and-evaluation-boundary.md) (feedback)

## Context

Agent Harness platforms orchestrate multi-step LLM runs with tool calls, memory,
retries, and verification. Enterprises want the gateway to participate in Agent
governance — budgets, kill switches, audit, tool visibility — but the gateway is
not an Agent runtime. Conflating the two would pull execution loops, tool
protocols, memory, and orchestration into a product whose core job is trusted
routing, accounting, and governance.

The current codebase has no Agent, Run, or tool-execution entities. `agent_type`
is a User-Agent-inferred observation tag, not a trusted identity. `session_id`
is a client-supplied best-effort routing and correlation key. Neither was
designed to carry authorization, hard budget enforcement, or safety guarantees.

## Decision

### Agent is a controlled label, not an authorization principal

In this phase, an **Agent label** is an Application-scoped, operator-controlled
metadata attribute used for attribution, filtering, and policy targeting. It is
distinct from the User-Agent-inferred `agent_type` observation tag:

- `agent_type` remains an inferred observation label for analytics only;
- a controlled Agent label is explicitly assigned and may participate in
  statistics and soft policy, but is not an independent authorization principal.

Agent identity as a delegated Principal — with its own credentials, budget,
tool permissions, and trust chain — is deferred until concrete delegation
requirements exist. ADR-0051 already records this deferral.

### Run is an untrusted statistical projection of client sessions

A **Run Summary** is a read-only aggregation of gateway events sharing the same
client-supplied session identifier. It provides:

- per-session cost and token totals;
- request count, error rate, and latency distribution;
- soft threshold alerts when configured.

Because the client controls the session identifier, Run Summary is explicitly
**not** a trusted boundary:

- it cannot enforce a hard per-run budget;
- it cannot serve as an authorization scope;
- it cannot guarantee that a kill switch has stopped all related requests,
  because a client can change session identifiers between calls.

Run Summary exists for cost visibility and anomaly detection, not for safety
enforcement.

### Kill switch operates on trusted boundaries only

Synchronous emergency stop is available only at trusted governance boundaries:

- **Tenant** disablement;
- **Application** disablement (stops all bound keys);
- **APIKey** revocation.

These boundaries are enforced through the existing authenticated identity path
and take effect within the key cache TTL. Session-level kill is not offered as a
safety control because the session identifier is forgeable; at most it can be a
best-effort signal for investigation.

### ToolCallAudit is a metadata-only contract

The gateway records a **ToolCallAudit** metadata event when tool-call information
is visible in a forwarded request or response. The event captures:

- request/session/application identity;
- tool name, call identifier, and outcome class (success/error), derived from
  the protocol-level tool-call structure already passing through the adapter;
- timing and the associated provider/endpoint.

ToolCallAudit does not execute tools, resolve tool endpoints, or store tool
payloads beyond what already transits the adapter. It is an observability and
audit contract.

### The gateway does not host MCP/A2A runtime, tool execution, or orchestration

This phase explicitly excludes:

- MCP or A2A protocol brokering, discovery, or serving;
- tool execution, OAuth delegation, or per-tool permission enforcement;
- Agent loop orchestration, memory, planning, or verification;
- run-level state machines, approval workflows, or queueing.

These are Harness platform responsibilities. The gateway provides the governance
and accounting substrate (identity, budget, routing, audit, feedback) that a
Harness platform consumes, not the Harness itself.

## Consequences

- Enterprises gain per-application and per-session Agent cost visibility without
  the gateway becoming an Agent runtime.
- Operators can stop a runaway Application through trusted disablement, but
  session-level stop is not promised as a safety guarantee.
- ToolCallAudit provides audit visibility without committing the gateway to tool
  execution or MCP/A2A hosting.
- Future Agent delegation, trusted Run identity, signed tool permissions, and
  MCP/A2A runtime require separate ADRs triggered by concrete enterprise demand.
- The boundary keeps the gateway focused on routing, accounting, and governance
  while leaving room for a Harness platform to consume gateway contracts.

## Alternatives considered

### Agent as a first-class Principal with delegated credentials

Deferred. It requires a delegation trust chain, Agent-scoped credentials, tool
permissions, and budget semantics that no concrete enterprise requirement
justifies yet. Premature modeling would force a large authorization redesign.

### Trusted Run identity with signed tokens

Deferred. It requires a Harness-to-gateway token issuance protocol, key
management, and run-level budget reservation. Until a real Harness integration
exists, the client session identifier provides statistical visibility without
the cost of a new trust infrastructure.

### Session-level kill switch

Rejected as a safety control. A client can change or omit the session identifier,
so a session-level kill cannot guarantee stopping all related requests. It may be
added as a best-effort investigation aid, but not advertised as a safety
boundary.

### Gateway-hosted MCP/A2A broker

Rejected for this phase. It would expand the data plane into tool discovery,
execution, and protocol brokering — a different product surface. The gateway
remains protocol-faithful forwarding plus audit.

### Gateway-hosted evaluation and orchestration

Rejected. Evaluation execution, experiment tracking, and orchestration belong to
the Harness or an external evaluation platform. The gateway provides the
FeedbackEvent ledger (ADR-0054) and Dataset lineage (ADR-0055) as substrates,
not the execution layer.
