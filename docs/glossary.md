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
- **Pricing** — per-million-token rates used to compute cost from Usage. Granularity
  (alias vs per-provider) is deferred to step 6. (See ADR-0012; supersedes
  ADR-0004.)

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
  first byte is sent to the client; after that, errors propagate. Failed
  attempts are not billed. (See ADR-0011.)
- **Routing strategy** — how a Route picks among candidate providers: `priority`
  (first healthy), `weighted` (per-route/provider weight), `round_robin`
  (per-instance cursor in P0). (See ADR-0011.)
- **Circuit / health state** — per-provider healthy/unhealthy state that
  failover consults to skip bad providers; in-memory and per-instance in P0
  (like rate limiting). (See ADR-0011.)

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
- **Circuit breaker** — in-memory consecutive-failure tracker that marks a
  provider unhealthy with a cooldown (half-open on expiry); per-instance in P0.
  (See ADR-0011.)

## Billing & quota

- **Cost** — money/points for a request: prompt/1_000_000×PromptPer1M +
  completion/1_000_000×CompletionPer1M, using the actually-hit provider's
  ModelUpstream.Pricing (aligns with llm.provider; failover bills the serving
  provider). (See ADR-0012.)
- **Quota** — a *balance* (total spend until reset/top-up, NOT self-recovering),
  distinct from the TPM *rate* limit. Denominated in cost (money), checked
  allow-then-debit at ingress (reject only if already ≤ 0; debit real cost after
  the response). (See ADR-0012.)
- **Quota store** — the shared, strongly-consistent backend (PG row update /
  Redis atomic) holding balances. Required from P0 because quota is money;
  multi-instance overspend is not acceptable — diverges from the in-memory
  default baseline. (See ADR-0012.)
- **Usage record** — an audit/reconciliation row (`usage_records`) written
  async/batched to PG; separate from the fast quota debit. A crash may lose a
  not-yet-flushed record (under-bill) but must not corrupt the balance.
  (See ADR-0012.)
- **Partial-stream billing** — when a stream drops before the trailing usage
  chunk, bill the usage actually received (best-effort, never zero when content
  was delivered, never fabricated). (See ADR-0012.)
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

- **request_id** — gateway-assigned (or upstream-propagated) per-request correlation ID. Injected as OTel span attribute, stored in `request_logs.request_id`. Priority: incoming `X-Request-Id` / `X-Trace-Id` / `traceparent` headers → chi middleware auto-generated UUID. Configurable via `WithTraceHeaders()`. Not a primary key and not unique — clients may reuse it within a session; trace detail lookups use the `trace_payloads.id` auto-increment primary key instead.
- **upstream_request_id** — provider-assigned request ID returned in the upstream response (OpenAI `x-request-id` header, Anthropic `request-id` header/body, Gemini `x-goog-request-id`). Extracted by the Forwarder from `resp.Header` (with adapter body fallback), stored in `request_logs.upstream_request_id` (indexed for reverse lookup). Captured for the final/successful attempt only — per-attempt capture including failed retries is a follow-up. Used for support/reconciliation to map a gateway request to the provider's side. Never echoed to external clients.
- **session_id** — client-supplied session key extracted from the `X-Voxeltoad-Session` header (or configured `sessionHeaders`). Stored in `request_logs.session_id` with a `(session_id, created_at)` index, enabling per-session request chain queries via `GET /api/v1/request-logs?session_id=X`.
- **trace_id** — W3C trace id parsed from the `traceparent` header (the `00-<trace_id>-<span_id>-<trace_flags>` format). Stored in `request_logs.trace_id` and `trace_payloads.trace_id` (both `DEFAULT ''`). The gateway does NOT emit a synthetic `llm.trace_id` OTel span attribute — trace_id is a W3C standard carried by the OTel trace context itself, so a separate attribute would be redundant. Empty when the client sent no `traceparent` or it was malformed. Unlike `request_id`, trace_id is structurally unique per W3C spec, but the gateway does not enforce uniqueness at the DB level (it relies on the W3C contract). Captured alongside `X-Trace-Id` as a secondary trace-correlation header; see ADR-0040 for the entry-id resolution chain.
- **request_logs** — the data-plane per-request audit ledger. One row per LLM request (success or rejection), written asynchronously fail-open. Read API: `GET /api/v1/request-logs` (offset paginated, CSV exportable). Distinct from `usage_records` (billing) and `audit_logs` (management-plane mutations). (See ADR-0021.)

## Engineering environment

- **POSIX-only** — a script or tool that runs only on POSIX-compatible environments (Linux / macOS / WSL2) and is unavailable on Windows native (PowerShell / cmd). Typically signals use of bash builtins plus utilities such as `setsid` / `lsof` / `pgrep` / `jq` / `openssl` / `mktemp` / process-group negative PIDs. Most of `scripts/*.sh` is POSIX-only by design. (See ADR-0042.)
- **WSL2** — Windows Subsystem for Linux 2. The project's official development environment for Windows contributors; a full Linux userland (Ubuntu 22.04+) inside which all `make` targets including `make ci` are expected to run. (See ADR-0042.)
- **官方开发环境 (official dev environment)** — the set of OS × shell combinations the project explicitly supports and verifies in documentation. Currently: macOS native, Linux native, and Windows via WSL2. Git-Bash and PowerShell native are NOT official environments; issues arising there are out of scope. (See ADR-0042.)
- **target 分级清单 (target tier list)** — the README table that classifies each `make` target by its platform requirements: 原生跨平台 (cross-platform, only go/node/npm/git) / 需 bash + coreutils / 需 WSL2 (POSIX-only). Used by contributors to know which targets they can run on their machine. (See README「Windows 开发者」section, ADR-0042.)
