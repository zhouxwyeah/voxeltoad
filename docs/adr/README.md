# Architecture Decision Records (ADR)

Short, immutable records of significant design decisions. Each ADR captures the
context, the decision, and its consequences at a point in time. Supersede rather
than edit when a decision changes.

Produced and refined through `grill-with-docs` sessions.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-provider-type-vs-adapter.md) | Provider brand (Type) is separate from protocol Adapter | Accepted |
| [0002](0002-model-alias-resolution-in-routing.md) | Model alias→upstream resolution happens in the routing layer | Accepted |
| [0003](0003-apikeyref-secret-resolution.md) | APIKeyRef secret resolution (env/plain + extension point) | Accepted |
| [0004](0004-pricing-granularity.md) | Pricing granularity | Resolved (→ ADR-0012) |
| [0005](0005-tenancy-hierarchy.md) | Tenancy hierarchy: Tenant → Group → APIKey (three levels) | Accepted |
| [0006](0006-apikey-auth-and-data-channel.md) | API key auth: hashed keys, cache-first data channel, public KeyID | Accepted |
| [0007](0007-control-plane-trust.md) | Data-plane ↔ management-plane trust (shared secret) | Accepted |
| [0008](0008-rate-limiting.md) | Rate limiting: RPM+TPM, sliding window, multi-dim Limiter, in-memory P0 | Accepted |
| [0009](0009-request-normalization-layer.md) | Request normalization layer (max_tokens default, system extraction, alternation) | Accepted |
| [0010](0010-claude-adapter-stream-and-usage.md) | Claude adapter: self-judged stream end, usage assembly | Accepted |
| [0011](0011-routing-and-failover.md) | Routing strategies and failover semantics | Accepted |
| [0012](0012-billing-and-quota.md) | Billing & quota: cost units, quota vs rate limit, partial-stream billing, consistency | Accepted (quota access refined → ADR-0013) |
| [0013](0013-quota-data-plane-access.md) | Quota data-plane access: direct PG, pre-debit/settle, integer micro-units | Accepted |
| [0014](0014-management-plane-schema.md) | Management-plane schema: hybrid row+JSONB config, flat-scope quota, global config / tenant-scoped keys | Accepted |
| [0015](0015-migrations-and-snapshot-versioning.md) | Migrations & snapshot versioning: goose embedded, startup auto-migrate, config_generation counter | Accepted |
| [0016](0016-data-service-design.md) | Data-service design: repo ownership, multi-scope debit transaction, async usage recording | Accepted |
| [0017](0017-management-plane-rbac.md) | Management-plane RBAC: operator auth, scoped-repository isolation, bootstrap, audit | Accepted |
| [0018](0018-session-affinity-routing.md) | Session-affinity routing (HRW) for provider-side prompt-cache locality | Accepted |
| [0019](0019-control-plane-read-ops-apis.md) | Control-plane read/ops APIs: observability reads (keyset-paginated, time-bounded) + atomic quota top-up | Accepted |
| [0020](0020-control-panel-bff-auth-topology.md) | Control Panel front-end auth topology: Next.js BFF holds the operator token | Accepted |
| [0021](0021-request-logs-data-plane-audit-ledger.md) | request_logs: data-plane per-request audit ledger | Accepted |
| [0022](0022-wasm-plugin-extension.md) | WASM plugin extension point (wazero): ABI v1 contract, sandbox, lifecycle | Proposed |
| [0023](0023-external-moderation.md) | External moderation provider integration: ModerationProvider interface, OpenAI default | Accepted |
| [0024](0024-data-plane-node-heartbeat.md) | Data-plane instance registration & heartbeat: fail-open, zombie cleanup, status lifecycle | Accepted |
| [0025](0025-config-snapshot-history.md) | Config snapshot history: async save, diff, rollback, dry-run preview | Accepted |
| [0026](0026-rate-limit-instance-division.md) | Rate limit instance-count division: ceil-divide at startup, no-Redis interim | Accepted |
| [0027](0027-pii-redaction-plugin.md) | PII detection & redaction plugin: regex block/redact modes, privacy constraint | Accepted |
| [0028](0028-csv-export-endpoints.md) | CSV export endpoints: synchronous, 2000-row cap, UTF-8 BOM, Excel-compatible | Accepted |
| [0029](0029-monthly-partition-migration.md) | Monthly partition migration: PostgreSQL DO block, 12-month rolling window | Accepted |
| [0030](0030-config-patch-editing.md) | Config PATCH editing via SELECT FOR UPDATE (provider pilot) | Accepted |
| [0031](0031-provider-credential-encryption-at-rest.md) | Provider upstream credential encryption at rest (`db://provider/<name>`, AES-256-GCM) | Accepted |
| [0032](0032-openai-passthrough-fidelity.md) | OpenAI→OpenAI fidelity: passthrough, raw Content, and SDK strategy | Accepted |
| [0033](0033-data-plane-keys-not-bound-to-roles.md) | Data-plane API keys are a separate permission domain from management-plane roles | Accepted |
| [0034](0034-redis-backed-shared-state.md) | Redis-backed shared state for rate limiting, caching, and circuit breaking | Proposed |
| [0035](0035-database-connection-pool-tuning.md) | Database connection pool tuning (max open/idle/lifetime) | Proposed |
| [0036](0036-dynamic-rate-limit-division.md) | Dynamic rate-limit division via heartbeat online count | Proposed |
| [0037](0037-multi-instance-deployment-topology.md) | Multi-instance (cluster) deployment topology | Proposed |
| [0038](0038-node-lifecycle-status-management.md) | Node lifecycle & status management — gap analysis and recommendation | Diagnostic (no impl) |
| [0039](0039-llm-trace-payload-capture.md) | LLM trace payload capture — message & raw layer (`trace_payloads` ledger) | Accepted |
| [0040](0040-request-id-strategy.md) | Request ID strategy — client cooperation, non-uniqueness, upstream-id capture and isolation | Accepted |
| [0041](0041-desktop-gateway-orthogonality.md) | Desktop personal gateway — same-repo, orthogonality argument, K1 seed-key auth | Accepted |
| [0042](0042-windows-dev-environment-wsl2.md) | Windows 开发者官方环境 = WSL2（脚本保持 POSIX-only，CI 仅 Linux） | Accepted |
| [0043](0043-windows-desktop-packaging.md) | Desktop Windows 打包 — Wails v2 NSIS + CI Windows runner（supersede ADR-0042 §3/§39 窄面） | Accepted |
| [0044](0044-merge-model-catalog-into-models.md) | 合并 model-catalog 到 models（单页 + 角色条件渲染） | Accepted |
| [0045](0045-anthropic-ingress-protocol.md) | Anthropic ingress protocol (`/v1/messages`) — inbound codec layer, Claude Code → OpenAI upstreams | Accepted |
| [0046](0046-ingress-protocol-persistence.md) | Persist `ingress_protocol` to audit ledgers (request_logs + trace_payloads) | Accepted |
| [0047](0047-protocol-aware-routing.md) | Protocol-aware routing — implicit passthrough via candidate reordering + RawProtocol gating | Accepted |
