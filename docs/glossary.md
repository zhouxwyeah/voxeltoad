# Glossary

Domain vocabulary for voxeltoad. Terms here have precise, agreed
meanings; use them consistently in code, config, docs, and observability. Built
and refined through `grill-with-docs` sessions.

## Routing & models

- **Alias** — the public model name a client requests (e.g. `default-chat`,
  `gpt-4o`). Mapped to an upstream model by the routing layer. Recorded as
  `llm.model.requested`. (See ADR-0002.)
- **Upstream model** — the provider-native model name sent upstream (e.g.
  `gpt-4o`, `claude-3-5-sonnet`). What an Adapter actually puts on the wire.
  Recorded as `llm.model.resolved`. `UnifiedRequest.Model` holds this value
  after routing.
- **Model (config)** — a config entry mapping one Alias → (Provider,
  UpstreamModel) plus Pricing. `internal/config.Model`.
- **Route** — config mapping an Alias to a list of candidate Providers and a
  selection Strategy (priority / weighted / round_robin). `internal/config.Route`.

## Providers & adapters

- **Provider** — a configured upstream LLM vendor instance (Name, brand Type,
  Endpoints, shared credential ref, timeouts, weight). A provider can carry
  multiple endpoints (ADR-0049) — e.g. a dual-protocol vendor with both an
  OpenAI-compatible and an Anthropic-compatible endpoint.
  `internal/config.Provider`.
- **Endpoint (provider endpoint)** — one (Adapter, BaseURL) pair under a
  provider (ADR-0049). The runtime selects the endpoint whose adapter matches
  the ingress protocol. Identified by `EndpointKey{Provider, Endpoint}` for
  breaker/audit purposes. `internal/config.ProviderEndpoint`.
- **Type (provider brand)** — descriptive brand label (`openai`, `tencent`,
  `zhipu`, `anthropic`). Does **not** select behavior; observability-facing.
  (See ADR-0001.)
- **Adapter (field)** — the protocol adapter key on a ProviderEndpoint:
  `openai` (shared by openai/tencent/zhipu/compatible) or `claude`.
  (See ADR-0001/0049.)
- **Adapter (component)** — a pure translator between the unified model and a
  provider's native protocol. Values-in/values-out: `BuildRequest →
  UpstreamRequest`, `ParseResponse([]byte)`, `ParseStream(io.Reader)`. Performs
  no HTTP transport. `internal/adapter.Adapter`.
- **UpstreamRequest** — transport-neutral description of the request to send
  upstream (Method/URL/Header/Body). Produced by an Adapter; the proxy turns it
  into an `*http.Request`. `internal/adapter.UpstreamRequest`.
- **Ingress protocol** — the wire protocol clients use to drive the gateway
  (`openai` for `/v1/chat/completions`, `anthropic` for `/v1/messages`). The
  ingress protocol is distinct from upstream Adapter: an Anthropic-ingress
  request can drive an OpenAI-adapter upstream (and vice versa).
- **Ingress codec** — a pure translator between a client wire format and the
  unified model on the inbound side (`DecodeRequest`/`EncodeResponse`/
  `NewStreamEncoder`/`EncodeError`); the inbound dual of Adapter. Values-in/
  values-out: no HTTP transport. `internal/ingress.Codec`. (See ADR-0045.)

## Transport & streaming

- **Data plane** — the stateless proxy serving the OpenAI-compatible API,
  running the plugin chain, routing, and forwarding. Owns HTTP transport,
  layered timeouts, and retries. `cmd/gateway`, `internal/proxy`.
- **Management plane** — the admin API persisting config to PostgreSQL and
  serving the config snapshot. `cmd/admin`, `internal/admin`.
- **Config snapshot** — the dynamic config the data plane polls from the
  management plane (`/internal/config/snapshot`, version/ETag). No etcd.
  `internal/config.Dynamic`.
- **Event (SSE)** — one decoded Server-Sent Event (`event`/`id`/`data`).
  `pkg/sse.Event`.
- **Done sentinel** — the `[DONE]` SSE payload marking clean stream termination;
  surfaced as a normal Event, not EOF, so truncation is distinguishable.
  `pkg/sse.Done`.
- **Chunk** — one unified streamed delta. Usage appears only on the trailing
  chunk(s); intermediate chunks have `Usage == nil`. `internal/adapter.Chunk`.
- **TTFT** — time to first byte/token; the latency until the first streamed
  chunk. Bounded by the FirstByte timeout; recorded as `llm.ttft_ms`.

## Secrets & billing

- **APIKeyRef** — a reference to an upstream credential: `env://VAR`,
  `db://provider/<name>` (encrypted at rest, resolved from `provider_credentials`),
  `plain://literal`, a bare literal, or a custom registered scheme. Resolved by
  `config.ResolveSecret`; the resolved key is never logged. (See ADR-0003,
  ADR-0031.)
- **Usage** — token accounting (prompt/completion/total) taken from the upstream
  response, never from a local estimate. `internal/adapter.Usage`.
- **Pricing** — per-upstream, per-million-token rates and cache multipliers used
  to compute cost from upstream Usage. Enterprise requests freeze the dispatcher
  candidate-price snapshot at reservation and use that same version at settlement.
  An explicit zero price is distinct from missing pricing; currencies must agree
  across reachable candidates and applicable funds. (See ADR-0012/0052.)

## Tenancy & client authentication

- **Tenant** — the top-level isolation boundary (e.g. a company/business unit).
  Recorded as `llm.tenant`. (See ADR-0005.)
- **Group** — a subdivision within a Tenant (e.g. a team) with its own
  budget/usage view and optional model scoping. Recorded as `llm.group`. The
  middle of the three tenancy levels. (See ADR-0005.)
- **APIKey** — the client credential the gateway issues (distinct from the
  upstream APIKeyRef). Belongs to one Group. Stored only as a hash; carries
  scope (allowed models, quota, rate limits, expiry). (See ADR-0006.)
- **KeyID** — an independent, public, human-readable identifier for an APIKey
  (e.g. `key_01H...`), used in logs/traces/audit as `llm.api_key_id`. The
  plaintext key and its hash are never logged. (See ADR-0006.)
- **Key cache** — the data plane's local, short-TTL cache of key records;
  authentication is cache-first with a fallback lookup on miss, so keys are
  real-time without bloating the config snapshot. (See ADR-0006.)
- **Application** — a long-lived, tenant-scoped business system or product that
  consumes AI. The stable workload identity for usage attribution and periodic
  budgets (routing/model-access policy remain future work); distinct from a Group (organizational
  consumer/owner), APIKey (credential), Agent Run (transient execution), and
  Workload Profile (per-request routing context). Has one owning Group but may be
  consumed by keys from multiple Groups in the same Tenant. (See ADR-0051.)
- **Application binding** — the trusted association from an APIKey (or a future
  server-resolved workload credential) to at most one Application. The data plane
  derives `application_id` from the authenticated credential; a caller-supplied
  header cannot establish this governance identity. New enterprise keys require
  same-tenant consuming Group + Application + dev/staging/prod. Legacy keys may
  complete missing identity once without changing nonempty fields; rebinding
  requires a new key. Desktop nil Application remains valid. (See ADR-0051.)
- **Unbound / unattributed** — `unbound=true` selects legacy keys with incomplete
  governance identity for repair (non-revoked, not necessarily unexpired or still
  callable); `unattributed=true` filters historical business ledgers missing
  application attribution. Key counts are not request percentages.
  Historical snapshots are never guessed from a later key binding.
- **Application environment** — a controlled credential attribute such as `dev`,
  `staging`, or `prod`, snapshotted into request/usage records. It does not create
  a separate Application or an `ApplicationDeployment` entity. (See ADR-0051.)
- **Internal trust secret** — the shared secret authenticating the data
  plane ↔ management plane channel (e.g. the config snapshot request), carried
  in bootstrap config and resolved via `config.ResolveSecret`. Distinct from
  client APIKey auth and upstream credentials. (See ADR-0007.)

## Rate limiting

- **RPM / TPM** — requests-per-minute / tokens-per-minute, the two rate-limit
  metrics. TPM tracks LLM cost/load that RPM cannot. (See ADR-0008.)
- **Allow-then-debit** — the TPM scheme: at ingress reject only if a dimension
  is already over limit; after the response, debit the *actual* `usage` tokens
  into the window. Avoids pre-estimating tokens at the cost of a small possible
  overshoot. (See ADR-0008.)
- **Sliding window** — the chosen rate-limit algorithm (window total), not a
  token bucket: smooths load toward the upstream (no burst pass-through) and
  matches the "quota within a window" model. (See ADR-0008.)
- **Dimension (rate limit)** — a scope+metric+limit+window tuple the limiter
  checks (e.g. tenant TPM 100k/1m). Limits exist per tenant/group/key
  (ADR-0005). `internal/plugin/ratelimit`.
- **Limiter** — the rate-limit interface: `Allow(dims, n) → Decision` (with
  `RetryAfter`) plus `Debit(dims, n)` for allow-then-debit. In-memory in P0
  (single-instance only); Redis-backed for multi-instance correctness.
  (See ADR-0008.)

## Routing & multi-provider

- **Normalization layer** — runs after routing, before the adapter; makes a
  valid OpenAI request valid for the target provider without burdening adapters
  with rewriting: injects `max_tokens` default, merges consecutive same-role
  turns (Claude alternation), handles multi/mid system messages. Adapters stay
  pure translators. (See ADR-0009.)
- **DefaultMaxTokens** — per-`Model` config value the normalization layer
  injects when a request omits `max_tokens` (required by Claude). (See ADR-0009.)
- **Failover** — trying a backup provider on a retryable upstream failure
  (connection/timeout/5xx only; never 4xx). Streaming fails over only before the
  first byte is sent to the client; after that, errors propagate. Failed attempts
  with uncertain charges keep the reservation unknown; a successful fallback does
  not prove the earlier attempt was free. (See ADR-0011/0052 implementation.)
- **Routing strategy** — how a Route picks among candidate providers: `priority`
  (first healthy), `weighted` (per-route/provider weight), `round_robin`
  (per-instance cursor in P0). (See ADR-0011.)
- **RoutingPolicy** — a versioned Application policy that first filters candidates
  by hard eligibility constraints, then applies an approved Quality Tier and an
  ordered objective (`cost_first`, `latency_first`, or `quality_first`) before the
  existing health/failover execution path. (See ADR-0053.)
- **Quality Tier** — an operator-approved candidate class such as `economy`,
  `balanced`, or `premium`. It is backed initially by catalog metadata and offline
  evaluation, not inferred from traffic, price, or latency. (See ADR-0053.)
- **RoutingDecision** — an immutable, metadata-only ledger record explaining a
  request's policy version, candidate set, rejection reasons, signal snapshot,
  selection, budget degradation, attempts, and final outcome. Stored separately
  from `request_logs` and joined through the gateway `request_id`.
  (See ADR-0053.)
- **Workload Profile** — bounded per-request routing context such as modality,
  estimated token shape, latency objective, quality floor, and capability needs.
  It is not an identity and does not contain raw prompt/completion bodies in the
  RoutingDecision ledger. (See ADR-0051, ADR-0053.)
- **Circuit / health state** — per-provider-endpoint healthy/open/half-open state
  that failover consults to skip bad endpoints; in-memory and per-instance in P0
  (like rate limiting). Endpoint granularity follows ADR-0049.

## Desktop distribution observability

- **GatewayRequest (gateway request)** — one client-visible LLM call accepted by the gateway. It is the denominator for desktop request count, final success rate, duration, TTFT, token usage, and estimated cost. Retries, failover attempts, and circuit-skipped candidates do not create extra GatewayRequests. Represented by one `request_logs` row/read projection. (See ADR-0057.)
- **DispatchStep (dispatch step)** — one ordered decision step while a GatewayRequest is distributed. A step is either `skipped` (a candidate was evaluated but no network call was made) or `attempted` (an upstream call was made with selection outcome `selected`, `retryable_failure`, or `terminal_failure`). For streaming, `selected` is the no-more-failover lock-in point, not final stream success; final delivery outcome belongs to the parent GatewayRequest. Steps preserve request-time facts so old paths are not reconstructed from current config. (See ADR-0057.)
- **UpstreamAttempt (upstream attempt)** — the `attempted` subset of DispatchSteps. Candidate skips are not attempts and must not inflate attempt count or attempt failure rate.
- **Request success rate** — successful GatewayRequests divided by completed GatewayRequests, using the final client-visible result. A request that succeeds after failover is successful.
- **First-attempt success rate** — the share of GatewayRequests whose first real UpstreamAttempt becomes serving and completes successfully. Circuit-skipped candidates do not count as attempts.
- **Preferred-candidate hit rate** — the share of GatewayRequests ultimately served by the route's highest-ranked configured candidate. A circuit skip lowers this rate even if the first real attempt succeeds.
- **Actual failover rate** — the share of GatewayRequests where a real UpstreamAttempt fails retryably and a later real attempt is made, derived from DispatchSteps. Distinct from the legacy `request_logs.fallback` boolean, which is candidate-index based and can include non-network config skips while omitting breaker-filtered/exhausted paths.
- **Attempt failure rate** — failed UpstreamAttempts divided by completed UpstreamAttempts, counting pre-selection failures and a serving stream that ends in an upstream stream error. Diagnostic of upstream/distribution quality; never used as the desktop headline success rate.
- **Passive provider health** — a desktop read projection derived from recent real request/attempt outcomes, latency, last successful use, and current circuit state. It is not an active availability guarantee and does not generate probe traffic.
- **Estimated cost** — a non-authoritative desktop statistic computed from locally configured Pricing and actual upstream Usage. It is always labeled as an estimate and is not a balance, budget, quota, invoice, or billing ledger.
- **SessionProjection (session projection)** — a read-only grouping of GatewayRequests sharing the derived `session_id`. Users can filter, inspect, and favorite it but cannot manually merge or split requests.
- **SessionFavorite (session favorite)** — local metadata keyed by `session_id` that pins a SessionProjection and exempts its request, DispatchStep, and TracePayload records from ordinary retention until removed. Distinct from a saved prompt template.
- **SetupReadiness (setup readiness)** — the desktop first-run read model for the Provider → Model → Route → Test dependency chain. It identifies the single next setup action; it is not persisted governance workflow.

## Claude specifics

- **Claude adapter** — translates the unified model to Anthropic's Messages API:
  `system` to top-level, `content[0].text` ↔ unified content, `stop_reason` ↔
  finish_reason, `input_tokens`/`output_tokens` ↔ prompt/completion. Detects
  stream end on `event: message_stop` (no `[DONE]`) and assembles usage from
  `message_start` (input) + `message_delta` (output) to honor the Chunk usage
  contract. (See ADR-0010.)
- **Dispatcher** — the routing/failover orchestrator above the Forwarder:
  resolves a route's ordered candidates, tries each (retrying retryable
  failures), tracks circuit health, and reports the actually-hit provider.
  `internal/proxy`. (See ADR-0011.)
- **Forwarder** — single-provider executor: one adapter + layered timeouts +
  http.Client; runs Forward / ForwardStream. Unaware of routing/failover.
  (See ADR-0011.)
- **Router (component)** — pure candidate ordering by strategy, minus
  breaker-unhealthy providers. (See ADR-0011.)
- **Circuit breaker** — in-memory consecutive-failure tracker keyed by
  `EndpointKey{Provider, Endpoint}`. It marks an endpoint open during cooldown,
  re-allows calls in half-open after expiry (without single-probe concurrency gating),
  and is per-instance in P0. (See ADR-0011/0049.)

## Billing & quota

- **Cost** — money/points for a request: prompt/1_000_000×PromptPer1M +
  completion/1_000_000×CompletionPer1M, using the actually-hit provider's
  ModelUpstream.Pricing (aligns with llm.provider; failover bills the serving
  provider). (See ADR-0012.)
- **Quota** — the retained non-recurring balance in `quotas`, not migrated into
  BudgetAccount in E1. Top-ups cannot change an existing scope's currency. Group
  scopes use `group:<tenant>/<group>` with each name path-escaped, not an ambiguous
  unqualified group name. Distinct from periodic budgets, rate limits, and Token
  Allowance. (See ADR-0012/0052 implementation clarification.)
- **Period spend control** — E1's cost control: atomically reserve an output-cost
  estimate, settle actual cost, and reject new requests after exhaustion. In-flight
  actual cost can exceed the reservation and limit; this is not a strict hard
  spending cap and has no promised fixed maximum overage.
- **BudgetPolicy** — current cost configuration: same-tenant scope
  (tenant/consuming Group/Application/Application+environment/Key), daily/weekly/
  monthly period, IANA timezone (UTC default, Monday week start), currency, limit,
  `soft`/`enforce`, thresholds, enabled state, and version. Only limit/enabled are
  mutable; no hard delete. Token Allowance, automatic degradation, and external
  notifications are not implemented in E1. (See ADR-0052.)
- **BudgetAccount** — one policy period with limit, reserved, committed, released,
  and start/end. `available = limit - committed - reserved` is derived, not stored.
  Enforce accounts reject insufficient reservations; soft accounts record the same
  spend and events without blocking. Accounts are created lazily; late settlement
  always uses the originally reserved period, not the current period.
- **Billing reservation** — a durable financial operation identified by a fresh
  server-generated ID, never by client_request_id. Freezes authenticated identity,
  currency, estimate, candidate-price snapshot and participating quota/account
  targets. Gateway request_id remains a correlation field. The AccountingStore
  coordinator reserves and settles legacy balances plus periodic accounts together.
- **Unknown charge** — an upstream call may have incurred costs but final Usage or
  charge cannot be confirmed. The reservation remains held; stale scanning only
  marks it for review, never refunds it. `released_unknown` is an explicit risk
  release, not a zero-cost fact; later known settlement applies only the required
  delta, without a second refund. Manual resolution requires global budget.resolve,
  version, operator, reason, and evidence.
- **Budget event** — durable threshold/overspend/unknown or manual-resolution
  evidence in `budget_events`; thresholds use actual committed spend and deduplicate
  by account and threshold. It is not an external notification delivery system.
- **Token Allowance** — a planned token-total boundary independent from cost and
  TPM. Not part of current E1; reliable upstream Usage, not a fabricated local
  count, would be its accounting fact. (See ADR-0052.)
- **ResourceUsage** — an accounting envelope whose source is explicitly either
  `provider_billed` or `self_hosted_compute`. Provider token/cost facts must not be
  misrepresented as GPU use; self-hosted allocation requires explicit resource
  measurements and an internal pricing rule. (See ADR-0052.)
- **Quota store** — the shared, strongly consistent PG balance backend. Enterprise
  AccountingRepo coordinates it with periodic accounts in one transaction;
  concurrent reservation must be atomic, while actual in-flight overage is allowed.
  No Redis accounting implementation is included in E1. (See ADR-0012/0052.)
- **Usage record** — an async/batched business report row (`usage_records`) for
  known Usage/cost, including app/env/currency snapshots. A crash may lose an
  unflushed row; reservation/account is the financial authority, not this report.
  Historical empty currency means unknown and must not be backfilled from current
  prices. (See ADR-0012/0052.)
- **Partial-stream billing** — settle only a confirmed usage/charge using frozen
  prices. A missing final Usage or uncertain failed attempt keeps the reservation
  unknown, without fabricated tokens or automatic zero-cost refund. (See ADR-0052
  implementation clarification.)
- **Completion hook** — the single point (non-streaming: after Forward;
  streaming: when the relay loop ends, including on drop) where TPM debit, quota
  debit, and the usage record are triggered together for consistency. Requires
  wiring the plugin chain into the forward path (deferred since step 4).
  (See ADR-0012.)

## RBAC & operators

- **Operator** — a human management-plane user (email + password argon2id hash), distinct from client API Key auth. Holds one Role. `internal/operator.Operator`.
- **Role** — a named set of Permissions + a scope kind (`global` or `tenant`). Built-in roles (`super-admin` / `tenant-admin`) are seeded by migration and marked `is_builtin`. Custom roles can be created via the API. Stored in `roles` table. (See ADR-0017, Phase-2.)
- **Permission** — a `resource.action` string (e.g. `provider.write`, `api_key.read`). Defined as Go constants in `internal/authz/permission.go`. The wildcard `*` means "all permissions" (carried only by the built-in super-admin role).
- **ScopeKind** — whether a role (and its holder) operates globally (`tenant_id IS NULL`) or within a single tenant (`tenant_id IS NOT NULL`). This is the structural isolation axis; scoped repositories enforce tenant boundaries and NEVER consult role names.
- **requirePermission(perm)** — Phase-2 middleware that authorizes a request by checking the calling operator's loaded permission set. Replaces the old `requireSuperAdmin()` / `requireTenantAdmin()` role-enum checks.
- **requireTenantScoped()** — Phase-2 structural gate that rejects operators with `tenant_id IS NULL` on tenant routes. Prevents global-scope operators from leaking across tenants irrespective of their permissions.
- **Data-plane key vs Role (distinct domains)** — A client **API key is NOT bound to a management-plane Role**. A key's access is expressed by data-plane-native dimensions only: `allowed_models` (which models), `group` (rate/quota), and `tenant_id` (isolation boundary). Roles govern *only* what a human operator may do in the control panel, and never leak onto data-plane credentials. If future data-plane capability gating (e.g. streaming / function_calling) is needed, it is a *separate* capability dimension on the key — not the role permission catalog. (See ADR-0033.)

## Request tracing

- **request_id** — gateway-generated per-request correlation ID (ADR-0050: always gateway-generated via chi middleware; never the client value). Injected as OTel span attribute `llm.request_id`, stored in `request_logs.request_id` / `trace_payloads.request_id`. Echoed back to the client via the `X-Request-Id` response header. Not a primary key — use `trace_payloads.id` for single-row trace detail lookups.
- **client_request_id** — client-supplied `X-Request-Id` header value (verbatim after trim; empty when the client sent none). Stored in `request_logs.client_request_id` / `trace_payloads.client_request_id` (migration 00027, ADR-0050) for cross-system correlation; NOT used as the primary correlation key because some agents (Claude Code, Codex) reuse the same id across every request in a session. Echoed back via the `X-Client-Request-Id` response header when present. Indexed for reverse lookup (`idx_request_logs_client_request_id`).
- **X-Trace-Id (request header)** — NOT read post-ADR-0050. The header was previously a request_id fallback when `X-Request-Id` was absent; after ADR-0050 the gateway ignores it entirely (not backed up, not adopted, not parsed as trace_id). Known clients all use `X-Request-Id` or W3C `traceparent`; no real consumer depends on `X-Trace-Id`. The response-side `X-Trace-Id` echo (from parsed traceparent) is a pre-existing asymmetry per ADR-0040. If a future agent depends on `X-Trace-Id`, introduce a dedicated field (e.g. `client_trace_id`) rather than overloading `client_request_id`.
- **upstream_request_id** — provider-assigned request ID returned in the upstream response (OpenAI `x-request-id` header, Anthropic `request-id` header/body, Gemini `x-goog-request-id`). Extracted by the Forwarder from `resp.Header` (with adapter body fallback), stored in `request_logs.upstream_request_id` (indexed for reverse lookup). Currently captured for the final/successful attempt only; ADR-0057 accepts per-attempt capture in DispatchStep, not yet implemented. Used for support/reconciliation to map a gateway request to the provider's side. Never echoed to external clients.
- **session_id** — client-supplied session key extracted from the `X-Voxeltoad-Session` header (or configured `sessionHeaders`). Stored in `request_logs.session_id` with a `(session_id, created_at)` index, enabling per-session request chain queries via `GET /api/v1/request-logs?session_id=X`.
- **trace_id** — W3C trace id parsed only from the `traceparent` header (the `00-<trace_id>-<span_id>-<trace_flags>` format). Stored in `request_logs.trace_id` and `trace_payloads.trace_id` (both `DEFAULT ''`). The gateway does NOT emit a synthetic `llm.trace_id` OTel span attribute because the OTel trace context already carries it. Empty when `traceparent` is absent or malformed. The request-side `X-Trace-Id` header is ignored after ADR-0050; the response-side `X-Trace-Id` echo from parsed trace context remains a historical asymmetry.
- **request_logs** — the data-plane per-request audit ledger. One row per LLM request (success or rejection), written asynchronously fail-open. Read API: `GET /api/v1/request-logs` (offset paginated, CSV exportable). Distinct from `usage_records` (billing) and `audit_logs` (management-plane mutations). (See ADR-0021.)

## Enterprise governance, data assets & harness

- **FeedbackEvent** — an immutable, tenant/application-scoped metadata record
  associating a gateway request or session with an outcome signal (metric, value,
  source, trust level, evaluator version, evidence ref). Does not copy prompt or
  completion bodies. The gateway owns the ledger and lineage; external evaluators
  own judge/experiment/training execution. (See ADR-0054.)
- **Dataset** — a named, tenant-scoped governed collection of examples with an
  owning Application or Group, data classification, and retention policy.
  (See ADR-0055.)
- **DatasetVersion** — an immutable snapshot of a dataset's item manifest. Edits
  produce a new version; versions are never mutated in place. (See ADR-0055.)
- **DatasetItem** — one example within a version, carrying provenance references
  to `request_logs`/`trace_payloads`/`FeedbackEvent` and a `PayloadRef` to stored
  content. A promoted item is a governed copy with its own retention independent
  of the trace ledger. (See ADR-0055.)
- **Payload Promotion** — the management-plane operation that copies a trace
  payload into a DatasetItem. Requires tenant opt-in, scoped permission, redaction
  check, and mutation/read/delete audit. (See ADR-0055.)
- **Agent Label** — an Application-scoped, operator-controlled metadata attribute
  for attribution and soft policy. Distinct from the User-Agent-inferred
  `agent_type` observation tag. Not an independent authorization principal in this
  phase. (See ADR-0051, ADR-0056.)
- **Run Summary** — a read-only aggregation of gateway events sharing a
  client-supplied session identifier, providing per-session cost, token, and error
  visibility. Because the session identifier is forgeable, Run Summary is a
  statistical projection, not a trusted safety boundary. (See ADR-0056.)
- **ToolCallAudit** — a metadata-only audit event recording tool-call visibility
  (tool name, call id, outcome class) from forwarded requests/responses. The
  gateway does not execute tools or host MCP/A2A runtime. (See ADR-0056.)
- **Kill Switch** — stop at a trusted governance boundary: Tenant disablement,
  Application disablement (all bound keys), or APIKey revocation. Takes effect at
  authentication within key-cache TTL (currently one minute by default), not an
  instant cross-instance invalidation or truncation of an in-flight stream.
  Session-level stop is not a safety control because session IDs are forgeable.
  (See ADR-0051/0056.)

## Engineering environment

- **POSIX-only** — a script or tool that runs only on POSIX-compatible environments (Linux / macOS / WSL2) and is unavailable on Windows native (PowerShell / cmd). Typically signals use of bash builtins plus utilities such as `setsid` / `lsof` / `pgrep` / `jq` / `openssl` / `mktemp` / process-group negative PIDs. Most of `scripts/*.sh` is POSIX-only by design. (See ADR-0042.)
- **WSL2** — Windows Subsystem for Linux 2. The project's official development environment for Windows contributors; a full Linux userland (Ubuntu 22.04+) inside which all `make` targets including `make ci` are expected to run. (See ADR-0042.)
- **官方开发环境 (official dev environment)** — the set of OS × shell combinations the project explicitly supports and verifies in documentation. Currently: macOS native, Linux native, and Windows via WSL2. Git-Bash and PowerShell native are NOT official environments; issues arising there are out of scope. (See ADR-0042.)
- **target 分级清单 (target tier list)** — the README table that classifies each `make` target by its platform requirements: 原生跨平台 (cross-platform, only go/node/npm/git) / 需 bash + coreutils / 需 WSL2 (POSIX-only). Used by contributors to know which targets they can run on their machine. (See README「Windows 开发者」section, ADR-0042.)
