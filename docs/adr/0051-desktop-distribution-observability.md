# ADR-0051: Desktop distribution observability — request-centric metrics and ordered dispatch steps

- Status: Accepted
- Date: 2026-07-31
- Builds on ADR-0011 (routing/failover), ADR-0021 (request ledger), ADR-0039 (payload capture), ADR-0041 (desktop orthogonality), ADR-0049 (provider endpoints)
- Revises ADR-0041 only at its "shared core remains unchanged" expectation: an optional observation seam may be added to the shared Dispatcher without changing routing behavior or requiring enterprise persistence.
- SoT: `design/desktop.md`

## Context

The desktop product was originally framed around passively recording Agent conversations to study prompts. The implemented product now also configures providers/models/routes, forwards real traffic, aggregates usage, and exposes request, trace, and runtime logs. Its next stage needs a sharper product boundary.

The chosen positioning is a local, lightweight personal gateway centered on **distribution + statistics**:

1. give multiple local LLM clients one stable endpoint and distribute traffic through model routing and failover;
2. show whether that traffic is healthy, where it went, and how much it used;
3. let the user explain an individual request through request logs, session traces, runtime logs, and its failover path.

The current request ledger records only the final provider, a `fallback` boolean, and the overall result. `RetryCount` is available transiently, but the ordered candidate path is not persisted. A successful failover therefore cannot answer which candidate failed or was skipped, why the gateway moved on, or how long each step took.

## Decision

### 1. Product boundary

Desktop prioritizes, in this order:

1. **Usability** — local-first, single-entry installation, guided Provider → Model → Route → Test setup, and no cloud account or external database.
2. **Distribution explainability** — request-level reliability metrics plus an ordered route/failover path.
3. **Local analysis and organization** — session/trace browsing, favorites, retention controls, and runtime diagnostics.

Distribution stops at configured model routing, endpoint selection, circuit-aware skipping, retry, and failover. Agent-specific policy, adaptive price/latency routing, budgets, quotas, RBAC, multi-tenancy, and other enterprise governance remain out of scope.

The process must remain idle-light: provider health is inferred passively from real traffic and circuit state. Desktop does not create periodic billable probe requests.

### 2. Three bounded contexts

#### Configuration

- **Provider** aggregate with one or more protocol-specific **Endpoints**.
- **ModelAlias** with upstream mappings.
- **Route** with strategy and ordered/weighted candidates.
- **SetupReadiness** read model for the four-step first-run flow.

#### Distribution

- **GatewayRequest** is one client-visible LLM call and the unit for request count, final success rate, duration, TTFT, token usage, and estimated cost.
- A GatewayRequest owns an ordered sequence of **DispatchSteps**.
- A DispatchStep is either:
  - `skipped`: a candidate was evaluated but no upstream call was made, for example because its circuit was open; or
  - `attempted`: an upstream call was made. Its **selection outcome** is `selected`, `retryable_failure`, or `terminal_failure`.
- An **UpstreamAttempt** is therefore the attempted subset of DispatchSteps. Candidate skips must not inflate attempt counts.
- `selected` means the candidate became the serving attempt. For streaming, this is the lock-in point after which failover is forbidden, not the final stream result. The parent GatewayRequest separately records the serving step ordinal and final delivery outcome (`completed`, `stream_error`, `client_cancelled`, or terminal request error).

Each finalized step carries immutable facts sufficient to explain the selection decision at that time: ordinal, provider, endpoint, action, skip reason or selection outcome, selection duration, retryability, upstream status/error class, and upstream request ID when available. The GatewayRequest carries the final client-visible delivery result. Historical records do not reinterpret old requests using current route configuration.

### 3. Shared Dispatcher exposes optional observation, not policy

The shared Dispatcher may publish DispatchStep events through an optional observer/sink contract. The contract:

- observes decisions already made by routing/failover;
- cannot alter candidate ordering, retryability, breaker state, or response handling;
- is fail-open and must not delay client traffic on persistence failure;
- is optional, so existing gateway behavior is unchanged when no observer is wired;
- allows desktop to persist steps in SQLite without requiring the enterprise PostgreSQL path to persist them in the same change.

This is a narrow revision to ADR-0041. Desktop remains a same-repo composition root and an enterprise data-plane subset; orthogonality is preserved because the extension is a generic observability contract rather than desktop policy in `internal/proxy`.

### 4. Metric semantics

- **Request success rate** is client-final: a request that succeeds after failover is successful.
- **First-attempt success rate** measures requests whose first real UpstreamAttempt becomes the serving attempt and completes successfully. Circuit-skipped candidates do not count as attempts.
- **Preferred-candidate hit rate** measures requests ultimately served by the route's highest-ranked configured candidate. A circuit skip lowers this rate even when the first real attempt succeeds.
- **Attempt failure rate** measures real UpstreamAttempts that fail selection or become the serving stream and then end in an upstream stream error. It is diagnostic and must not replace request success rate on the overview.
- **Actual failover rate** is derived from DispatchSteps: at least one real UpstreamAttempt has a retryable failure and a later real attempt is made. This is the precise desktop distribution metric.
- The existing `request_logs.fallback` boolean remains a legacy coarse signal: current code sets it when a request terminates on candidate index `i > 0` after breaker filtering. Earlier indices may have been skipped for missing forwarders or model/config mismatches without a network call; breaker-filtered candidates and exhausted requests are not represented consistently. It must not be treated as the source of truth once DispatchSteps exist.
- A configured preferred candidate removed before iteration by circuit filtering is a **preferred-candidate bypass**, not an actual failover.
- **Provider health** is a passive projection of recent DispatchSteps, request outcomes, latency, last successful use, and current circuit state.
- **Cost** is a secondary estimate computed from locally configured prices and actual upstream usage. It is labeled as estimated and does not create a balance, budget, quota, or billing ledger.

### 5. Session and payload semantics

A **Session** remains a read projection over GatewayRequests sharing the derived session key; users cannot manually merge or split requests. A **SessionFavorite** is separate local metadata keyed by session ID. Favoriting a session protects its request records, DispatchSteps, and trace payloads from normal retention until the favorite is removed.

Desktop keeps full Trace payload capture enabled by default for its single-user local use case, while clearly exposing the sensitivity, retention period, capture toggle, and destructive cleanup controls. Users can delete one session, immediately purge data before a chosen time, or clear all observation records while preserving gateway configuration.

This desktop default does not change ADR-0039's enterprise default-off policy.

## Consequences

- Overview and statistics use one client request as their denominator; retries and candidate skips cannot inflate traffic volume.
- Failover becomes explainable without scraping process logs or reconstructing old route configuration.
- The desktop store gains DispatchStep persistence and SessionFavorite metadata; the request projection also persists nullable serving-step ordinal and final delivery outcome so streaming results survive restart. Cleanup and retention must preserve referential behavior across request, step, and trace records.
- The UI needs a first-run readiness flow, passive health summaries, request drill-down into the ordered distribution path, and explicit estimated-cost labeling.
- The current desktop Provider UI must align with ADR-0049's `endpoints[]` contract before new analysis features, because configuration usability has first priority.
- The selected implementation order is: **usability → explainability → organization**.

## Rejected alternatives

- **Keep only final provider + fallback boolean** — too little information for the chosen log-analysis goal.
- **Store every upstream attempt as an independent request** — corrupts request count, success rate, duration, and session semantics.
- **Treat circuit-skipped candidates as upstream attempts** — claims network work that never happened and distorts attempt reliability.
- **Actively probe providers on a timer** — adds billable traffic and idle overhead, conflicting with the lightweight boundary.
- **Make per-attempt PostgreSQL persistence mandatory immediately** — unnecessarily couples desktop product work to enterprise governance and storage scope.
