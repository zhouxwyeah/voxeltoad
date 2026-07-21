# Observability Guide

> LLM 网关比普通网关多一层「语义可观测性」。本文件把「每条请求必须记录什么」固化为强制约束。
> 技术栈：OpenTelemetry（trace + metric）+ 结构化日志 + Prometheus + Loki。

## 为什么单独立约束

普通网关只关心 HTTP 状态码与延迟；LLM 网关的运营、计费、容量规划、问题排查都依赖**语义字段**（哪个模型、烧了多少 token、首字多慢、走了哪个供应商）。这些字段极易在新增供应商/插件时被遗漏，因此把它作为门禁约束，类比 neutree 用 i18n tracker 守护翻译覆盖。

## 每条 LLM 请求必须记录的语义字段

所有字段定义集中在 `internal/observability/llm_attributes.go`，作为单一事实来源。新增供应商或改流程时，**不得绕过该 schema 自行打点**。

| 字段 | 说明 | 来源 |
|---|---|---|
| `llm.tenant` | 租户标识 | 鉴权层 |
| `llm.api_key_id` | API Key（脱敏后的 id，非明文） | 鉴权层 |
| `llm.model.requested` | 业务方请求的模型别名 | 请求 |
| `llm.model.resolved` | 路由后实际命中的供应商模型 | 路由层 |
| `llm.provider` | 实际命中的供应商（**降级后记实际值**） | 转发层 |
| `llm.stream` | 是否流式 | 请求 |
| `llm.tokens.prompt` | prompt token | 上游 usage |
| `llm.tokens.completion` | completion token | 上游 usage |
| `llm.tokens.total` | 总 token | 计费层 |
| `llm.ttft_ms` | 首字延迟（流式才有意义） | 转发层 |
| `llm.duration_ms` | 整体耗时 | 转发层 |
| `llm.cache.hit` | 是否命中缓存 | 上游 usage（CachedPromptTokens>0） |
| `llm.cache.tier` | 缓存层级（`upstream` / 预留 `gateway`） | 转发层 |
| `llm.cache.source` | 缓存来源（provider 名） | 转发层 |
| `llm.tokens.cached_prompt` | 命中缓存的 prompt token 数 | 上游 usage |
| `llm.plugin.blocked_by` | 被哪个插件拦截（敏感词/配额等），未拦截为空 | 插件链 |
| `llm.retry.count` | 重试次数 | 转发层 |
| `llm.fallback` | 是否发生供应商降级 | 路由层 |
| `llm.error.type` | 错误分类（auth/ratelimit/upstream/timeout…） | 错误层 |
| `llm.request_id` | gateway 分配（或上行透传）的请求关联 ID | entry middleware |
| `llm.upstream_request_id` | provider 返回的请求关联 ID（OpenAI `x-request-id` 头、Anthropic `request-id` 头/body 等） | forwarder |
| `llm.session_id` | 客户端传入的会话 key（`X-Voxeltoad-Session`） | 请求 header |
| `llm.agent_type` | 调用方 agent 类型（claude-code/codex/…，未知为空；纯可观测性，非治理维度，不作 metric label） | User-Agent / `x-<vendor>-session-id` |
| `llm.ingress.protocol` | 客户端入站协议（`openai` / `anthropic`，低基数枚举；纯可观测性，ADR-0045） | 路由路径（`/v1/chat/completions` / `/v1/messages`） |

### request_id 与 session_id 与 upstream_request_id

- **request_id**: 每条 LLM 请求的唯一标识。取值优先级：上行请求 header（默认 `X-Request-Id` → `X-Trace-Id` → `traceparent`，可配置 `WithTraceHeaders()`）→ chi middleware 自动生成。此值落库 `request_logs.request_id` 并注入 OTel span attribute，上联 trace 下联审计。注意：`request_id` 不是主键，也不是唯一键——客户端可能在同一 session 内复用，trace 详情查询首选 `trace_payloads.id`（自增主键）。
- **upstream_request_id**: provider 在响应中返回的请求关联 ID。OpenAI 在响应头 `x-request-id`；Anthropic 在响应头 `request-id` 和 body `request_id`（头优先，body 由 adapter 兜底）；Google Gemini 在 `x-goog-request-id`。由 Forwarder 在 `resp.Header` 上提取，覆盖 adapter 从 body 填的兜底值。落库 `request_logs.upstream_request_id`，支持按上游 ID 反查（`idx_request_logs_upstream_request_id`）。仅捕获最终成功尝试的 ID；失败尝试的 ID 需 per-attempt 表（阶段二）。不回显给外部客户端（安全考虑）。
- **session_id**: 客户端传入的会话 key，从 `X-Voxeltoad-Session`（可配置多 header）或 body identity 字段提取。落库 `request_logs.session_id`，索引 `(session_id, created_at)` 支持按会话聚合查询请求链路。
- **agent_type**: 由 User-Agent 子串匹配检测（缺失时回退到 `x-<vendor>-session-id` 反推），落库 `request_logs.agent_type` / `trace_payloads.agent_type` 并注入 OTel span attribute `llm.agent_type`。**只用于审计/trace/管理面会话聚合**，不参与限流、配额、计费、路由、熔断任何治理决策；唯一例外是诊断 counter `request_id_invalid_total{agent_type}`。

决策背景与消费侧约束见 [ADR-0040](../docs/adr/0040-request-id-strategy.md)。要点：

**为什么 `request_id` 不强制 UNIQUE**：客户端协作模型（ADR-0040 Decision 1）允许客户端在同一 session 内复用同一个 `X-Request-Id`，或 buggy/恶意客户端硬编码 ID。强制 UNIQUE 会拒绝合法复用场景，或迫使网关覆盖客户端 ID（让回显的 `X-Request-Id` 说谎）。代价是 `request_logs.request_id` 可能有重复行——由下面的消费侧约束兜底。

**消费侧硬约束**：任何按 `request_id` 取**单条** trace 详情的代码，重复场景下必须用 `trace_payloads.id`（自增主键）点查 `TracePayloadQueryRepo.GetByRowID(rowID int64)`（`internal/store/tracepayload_query.go:120-145`），而非 `GetByRequestID`（`WHERE request_id = ? LIMIT 1`，重复时总是返回同一行）。前端 session 详情页已经走主键：`fetch-detail.ts:24` 调 `/api/v1/trace/rows/{id}`。`GetByRequestID` 仍保留用于 support/reconciliation 的"按客户端 ID 反查"场景（配合时间窗或 session 过滤）。

**不回显上游 ID 给客户端**：`echoCorrelationHeaders`（`internal/proxy/router.go:61-71`）只回显 `X-Request-Id` / `X-Voxeltoad-Session` / `X-Trace-Id` 三个网关自己的关联头。上游 `req_xxx`（OpenAI/Anthropic/Gemini 的内部 ID）永远不暴露给外部客户端——安全考虑（避免泄露上游基础设施拓扑）+ 行业惯例（Nginx/HAProxy/AWS ALB 都不透传后端 ID）。上游 ID 已落库 `request_logs.upstream_request_id`，客户端报障时运维可查。

**重试/failover 的多上游 ID**：当前 `upstream_request_id` 只捕获**最终成功**那次尝试的 ID（ADR-0040 Decision 5）。失败/重试/failover 尝试的上游 ID 不捕获——这会破坏 `request_logs` "1 请求 = 1 行"的账本模型（ADR-0021 §5）。per-attempt 捕获（含失败重试）是阶段二增强，见 `docs/ops/failover-troubleshooting.md §8`。

## Trace 约定

- 每条请求一个根 span，关键阶段（鉴权、限流、插件链、路由、上游转发、响应适配）各开子 span。
- 上游转发 span 必须记录 `llm.provider` 与 `llm.ttft_ms`。
- 流式请求：首 chunk 到达时记录 TTFT 事件；流结束时记录 usage 事件。
- **`request_id`**: span attribute `llm.request_id` 取自上行 trace header 或 gateway 生成，用于跨服务串联。当上游传入 `X-Request-Id` / `X-Trace-Id` / `traceparent` 时，gateway 使用该值作为 `request_id`；否则用 chi 中间件自动生成的 UUID。
- **`upstream_request_id`**: span attribute `llm.upstream_request_id` 取自上游响应头（或 body 兜底），用于售后/对账时定位到 provider 侧的请求记录。仅记录最终成功尝试的 ID。

## Metric 约定（Prometheus）

至少暴露：

- `llm_requests_total{tenant,model,provider,status}` —— 计数
- `llm_tokens_total{tenant,model,provider,type=prompt|completion}` —— 计数
- `llm_ttft_seconds{provider}` —— 直方图
- `llm_request_duration_seconds{provider,stream}` —— 直方图
- `llm_upstream_errors_total{provider,error_type}` —— 计数
- `llm_cache_hits_total{tenant,model,provider}` —— 计数
- `llm_ratelimit_rejected_total{tenant}` —— 计数
- `request_logs_dropped_total` —— 计数（异步写入失败/buffer 满丢弃，[ADR-0021](../docs/adr/0021-request-logs-data-plane-audit-ledger.md)）
- `trace_payloads_dropped_total` —— 计数（trace payload 异步写入失败/buffer 满丢弃，[ADR-0039](../docs/adr/0039-llm-trace-payload-capture.md)）
- `request_id_invalid_total{agent_type,tenant}` —— 计数（客户端传入 nil/zero request-id 被网关重生成）
- `session_id_invalid_total{source,tenant}` —— 计数（客户端传入的 session id 未通过 `validateSessionID`，按提取来源标注）

> 外部审查（moderation）拦截不通过独立计数器暴露，而是经 `llm.plugin.blocked_by` 语义字段记录（[ADR-0023](../docs/adr/0023-external-moderation.md)）。

**禁止**用裸 Prometheus 计数器零散拼凑语义字段；统一经 `internal/observability` 封装，保证 label 一致。

## 日志约定

- 结构化 JSON 日志，字段名与上表语义字段对齐。
- **严禁记录** prompt/completion 正文明文到普通日志（隐私 + 体积）。`request_logs`（[ADR-0021](../docs/adr/0021-request-logs-data-plane-audit-ledger.md)）也只记语义元数据（token 计数、耗时、错误分类），**不存正文**。
- 如需审计/追踪**对话正文与原始 payload**，走独立的 `trace_payloads` 受控账本（[ADR-0039](../docs/adr/0039-llm-trace-payload-capture.md)）：默认关闭、按配置 opt-in 全量捕获、fail-open 异步落库、短留存、读取受 RBAC + tenant 隔离 + 读取审计约束。两者严格分离——`request_logs` 永不携带正文。
- API Key、供应商密钥一律脱敏，不入日志/trace。

## 门禁约束

- 新增供应商适配器的 PR：集成测试必须断言上述语义字段被正确填充（尤其 `provider`、`tokens.*`、`ttft_ms`）。
- 新增会中断请求的插件（拦截类）：必须设置 `llm.plugin.blocked_by`。
- code review checklist 包含「是否经 `internal/observability` 打点、有无绕过 schema」。

---

## 演进路线

### 当前实现现状

- **Trace**：OTel SDK 已接入，每条请求一个根 span，关键阶段子 span 已覆盖鉴权/限流/插件链/路由/转发/适配。
- **Metric**：Prometheus exporter 已暴露 `llm_requests_total` / `llm_tokens_total` / `llm_ttft_seconds` 等核心指标，经 `internal/observability` 统一封装。
- **Log**：结构化 JSON 日志，字段名与语义字段对齐，严禁记录 prompt/completion 正文。
- **Audit**：`request_logs` 表（ADR-0021）记录语义元数据；`trace_payloads` 表（ADR-0039）受控捕获正文（默认关闭）。

### Metrics 演进（Prometheus）

**当前**：核心 LLM 指标已暴露，label 规范由 `internal/observability` 封装保证一致。

**未来方向**：
1. **业务指标**：按租户/模型维度的成本估算（`llm_cost_total{tenant,model,provider}`），依赖定价配置。
2. **饱和度指标**：PG 连接池使用率、Redis 连接池使用率（多实例时）、embedded PG 启动耗时（测试环境）。
3. **SLO 指标**：`llm_slo_success_rate` / `llm_slo_ttft_p99` 按租户维度，用于 SLA 报表。

**命名规范**：
- 所有指标以 `llm_` 前缀开头。
- label 顺序固定：`tenant` → `model` → `provider` → `status`/`type`/`stream` 等修饰 label。
- 禁止在 label 中使用高基数值（如 `request_id`、`session_id`），这些只能放 trace attribute。

**Exporter 位置**：`internal/observability/exporter.go` 统一注册，避免散在各 handler。

### Tracing 演进（OpenTelemetry）

**当前**：OTel SDK 已接入，span attribute 经 `internal/observability/llm_attributes.go` 统一 schema。

**未来方向**：
1. **分布式 trace 传播**：支持 `traceparent` / `tracestate` header 透传到上游（当前仅接收不传播）。
2. **Tail-based sampling**：按 `llm.error.type` / `llm.plugin.blocked_by` 采样，非 100% 全量。
3. **Trace 与日志关联**：`trace_id` 注入结构化日志，支持从日志跳转到 trace。
4. **Trace 与 metric 关联**：exemplar 支持，从 metric 跳转到 trace。

**接入点**：`internal/observability/tracer.go` 统一配置 OTel SDK，避免散在各 handler。

### Logging 演进

**当前**：结构化 JSON 日志，字段名与语义字段对齐。

**未来方向**：
1. **采样策略**：非错误日志按 `tenant` + `model` 维度采样（如 1%），错误日志 100% 保留。
2. **日志级别动态调整**：支持运行时按 tenant 调整日志级别（debug/info/warn/error）。
3. **日志聚合**：Loki 查询模板固化到 `docs/ops/log-queries.md`，覆盖常见排障场景。

### 审计演进

**当前**：`request_logs` 记录语义元数据；`trace_payloads` 受控捕获正文。

**未来方向**：
1. **审计查询 API**：支持按 `llm.session_id` / `llm.agent_type` / `llm.error.type` 聚合查询。
2. **审计告警**：异常模式检测（如某 tenant 错误率突增、某 API Key 调用频率异常）。
3. **合规报表**：按租户/时间范围导出审计日志（CSV/JSON），支持数字签名防篡改。
