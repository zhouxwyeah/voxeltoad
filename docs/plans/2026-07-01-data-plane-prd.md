# PRD：voxeltoad 数据面（Data Plane）

- 状态：数据面核心功能已完成并通过端到端验证（截至 2026-07-01）
- 范围：本文档只描述**数据面**（`cmd/gateway` 运行的请求代理）。管理面（admin CRUD/RBAC）另见 ADR-0014/0017。
- 关联设计：[llm-gateway-design](2026-06-29-llm-gateway-design.md)、ADR-0001~0018、`design/architecture.md`
- 一句话定位：一个 OpenAI 兼容的企业级 LLM 网关数据面——鉴权、按模型别名路由/故障转移到多家供应商、配额预扣与结算、异步用量落库、配置热更新，全部经真实 PG 持久化并有 e2e 覆盖。

---

## 1. 背景与目标

### 1.1 背景
数据面是无（本地）状态的请求代理：它轮询管理面的配置快照、直连配额库（钱，需强一致），把客户端的 OpenAI 兼容请求路由到实际供应商并归一化协议。它与管理面通过配置快照间接通信，不共享进程内状态（`design/architecture.md`）。

### 1.2 目标（本阶段已达成）
- 对外提供 **OpenAI 兼容** 的 `/v1/chat/completions`（流式 + 非流式）。
- 统一代理多家供应商（OpenAI / Claude / 腾讯混元 / 智谱 / 任意 OpenAI 兼容），按模型别名解析上游并做协议归一化。
- 企业治理：客户端密钥鉴权、按 key 的模型授权、配额预扣-结算、限流/缓存插件框架、异步用量审计。
- 配置热更新（不重启、秒级传播）。

### 1.3 非目标（本阶段）
- 管理端 UI / 更细粒度 RBAC 角色（viewer）、OIDC SSO、PG 行级安全——均为 phase-2。
- `/v1/models`、`/v1/completions`、embeddings 等其余 OpenAI 端点（当前 501/未实现）。
- 跨实例共享的熔断/限流状态（当前为单实例内存态）。

---

## 2. 系统边界与请求链路

```
Client (OpenAI SDK / HTTP)
   │  POST /v1/chat/completions  (Bearer <client-key>)
   ▼
┌──────────────────────── 数据面 gateway ────────────────────────┐
│ 1. auth 中间件      客户端密钥鉴权（缓存优先，PG 兜底）           │
│ 2. 模型授权         allowed_models 校验（不在白名单 → 403）       │
│ 3. plugin.Pre       billing 配额预扣（不足 → 402；库不可达 → 503）│
│ 4. dispatcher       按路由策略选候选 → 逐个尝试 + 故障转移        │
│      └ preparer      别名→上游模型名 + 协议归一化（每候选）        │
│      └ forwarder     经对应 adapter 转发到上游（分层超时）        │
│ 5. plugin.Post      billing 结算（est−actual）+ 异步记录用量      │
│ 6. 响应             OpenAI 兼容 JSON / SSE                        │
└────────────────────────────────────────────────────────────────┘
      ▲ 轮询配置快照                    │ 直连（钱，强一致）
      │                                 ▼
  管理面 /internal/config/snapshot   PostgreSQL（quotas / usage_records / api_keys）
```

配置数据经**轮询 + 原子替换**（最终一致，秒级）；配额经**直连 PG**（强一致，热路径预扣/结算，不可达 fail-closed）。这是 ADR-0013 的核心取舍。

---

## 3. 功能需求与实现状态

### 3.1 API 兼容性

| 能力 | 状态 | 说明 |
|---|---|---|
| `POST /v1/chat/completions` 非流式 | ✅ | 返回 OpenAI 结构：`choices[].message.content` + `usage` |
| `POST /v1/chat/completions` 流式（SSE） | ✅ | 逐块转发、每块 flush（保 TTFT）、尾部 usage 块、始终以 `[DONE]` 收尾（即使上游中断，客户端不挂） |
| 统一响应再编码 | ✅ | 不透传上游字节，统一重编码为 OpenAI 结构，屏蔽供应商差异 |
| 错误信封 | ✅ | `{"error":{"message","type"}}`，OpenAI 兼容 |
| `GET /v1/models` | ❌ 501 | 未实现（phase-2） |
| `/v1/completions`、embeddings 等 | ❌ | 未路由（phase-2） |
| `GET /healthz` | ✅ | 不鉴权 |

**HTTP 状态码契约**（`internal/proxy/router.go`）：

| 场景 | 状态码 | error.type |
|---|---|---|
| 缺失/非法/无效/过期密钥 | 401 | authentication_error |
| 模型不在 key 的 allowed_models | 403 | permission_error |
| 配额不足 | 402 | insufficient_quota |
| 配额库不可达（fail-closed） | 503 | api_error |
| 限流拦截（默认插件拒绝） | 429 | rate_limit_error |
| 上游超时 | 504 | timeout_error |
| 上游失败/全部候选失败 | 502 | upstream_error |
| 请求体非法 | 400 | invalid_request_error |

### 3.2 模型路由与故障转移

| 能力 | 状态 | 位置 |
|---|---|---|
| 别名解析（client model → 供应商上游模型名） | ✅ | `preparer.go`（ADR-0002） |
| 协议归一化（每候选，支持跨协议 failover，如 OpenAI↔Claude） | ✅ | `normalize` + preparer（ADR-0009） |
| 路由策略 priority | ✅ | `router_select.go` |
| 路由策略 round-robin | ✅ | 同上 |
| 路由策略 weighted（按权重随机） | ✅ | 同上 |
| 路由策略 session_affinity（同会话粘同供应商，HRW 哈希） | ✅ | `affinity.go` + `router_select.go`（ADR-0018，提升供应商侧 prompt 缓存命中/降本） |
| 会话键提取（配置头 > `prompt_cache_key`/`user` > 前缀哈希兜底） | ✅ | `affinity.go`；头名可配（默认 `X-Voxeltoad-Session`），新 agent 靠配置接入 |
| 跨供应商故障转移（仅对可重试错误） | ✅ | `dispatcher.go`（ADR-0011） |
| 非可重试（如 4xx）不转移，直接返回 | ✅ | 同上 |
| 流式故障转移（仅首字节前） | ✅ | `ForwardStream`（首字节后锁定供应商） |
| 熔断器（closed→open→half-open，按供应商） | ✅ | `breaker.go`（**单实例内存态**，多实例共享为 phase-2） |
| 上报实际命中供应商（计费/`llm.provider`） | ✅ | dispatcher 返回 provider |

### 3.3 供应商适配器

| 供应商 | 状态 | 说明 |
|---|---|---|
| OpenAI | ✅ | 完整（BuildRequest/ParseResponse/ParseStream/ExtractUsage） |
| Claude（Anthropic Messages） | ✅ | 完整，含 system 提升、消息交替归一化（ADR-0009） |
| 腾讯混元 / 智谱 / 任意 OpenAI 兼容 | ✅ | 复用 openai adapter（配置 `adapter: "openai"` + 各自 BaseURL/密钥，ADR-0001） |
| adapter 注册表 | ✅ | `init()` 自注册；`app` 组合根 blank-import 触发注册；`adapter.New(name, adapter.Options)` 泛型构造 |

### 3.4 鉴权与授权（客户端密钥）

| 能力 | 状态 | 说明 |
|---|---|---|
| Bearer 密钥鉴权 | ✅ | 仅存 SHA-256 hash，明文不落库（ADR-0006） |
| 缓存优先 + PG 兜底查找 | ✅ | 正/负缓存 TTL，限制回源与无效密钥洪泛 |
| 过期校验 | ✅ | `expires_at` |
| **按 key 的 allowed_models 授权** | ✅ | 非空则请求模型必须在列表内，否则 403；空=不限（本阶段修复的真实漏洞） |
| 身份注入（tenant/group/keyID）供下游插件 | ✅ | 经 request context |
| 操作员鉴权 | —（管理面） | 与客户端密钥完全独立，见 ADR-0017 |

### 3.5 计费、配额与用量

| 能力 | 状态 | 说明 |
|---|---|---|
| 成本计算（int64 微单位，一次性四舍五入） | ✅ | `cost.go`，无浮点漂移（ADR-0013） |
| 配额预扣（Pre，全或无跨 scope） | ✅ | 估算 = effectiveMaxTokens × 候选最大 completion 费率（completion-only 上限，ADR-0013） |
| 配额结算（Post，总是结算 est−actual） | ✅ | 无用量 = 全额退款；上游失败也走 Post 退款 |
| fail-closed | ✅ | 配额库不可达 → 拒绝（钱不能在故障时放行） |
| 异步用量落库（fail-open，有界缓冲，丢弃+计数） | ✅ | `async.go` + PG `usage_records`（ADR-0016），race 干净 |
| 多级 scope（tenant/group/key） | ✅ | `scopesOf` |

> **配额预检边界（已闭合）**：Pre 估算取 `max_tokens` > 候选 `DefaultMaxTokens` > **全局 max-tokens 兜底上限**（默认 4096，`gateway.max_tokens_ceiling` 可配），保证只要有定价估算就不为 0，欠费租户不会因缺 token 计数而绕过配额预检。

### 3.6 插件框架

| 插件 | 实现 | 是否接入 gateway | 说明 |
|---|---|---|---|
| billing（配额/计费） | ✅ | ✅ 已接入 | Pre 预扣 + Post 结算 |
| ratelimit（RPM/TPM 限流） | ✅ | ✅ 已接入 | Pre 链最前（billing 之前），配置 `gateway.rate_limit` 启用；tenant/group/key 维度；超限 → 429（ADR-0008，单实例内存态）|
| cache（响应缓存） | ✅ | ❌ 未接入 | 代码就绪，opt-in，未在 `cmd/gateway` 实例化 |

插件链 Pre/Post 双相；Pre 可 `Stop` 短路（拒绝或缓存命中），并可带 `RejectStatus` 决定 HTTP 码。

### 3.7 配置热更新

| 能力 | 状态 | 说明 |
|---|---|---|
| 轮询管理面快照（If-None-Match/ETag → 304） | ✅ | `config.Poller` |
| 原子替换（`atomic.Pointer`） | ✅ | 读者永不见半更新态 |
| 按配置版本重建调度器并原子换入 | ✅ | `app.DispatcherWatcher`，构建失败保留 last-good |
| 计费定价读实时快照 | ✅ | 定价随配置变更即时生效 |

---

## 4. 关键设计取舍（摘自 ADR）

- **配置最终一致 / 配额强一致分离**（ADR-0013）：配置走快照轮询（秒级、原子替换），配额走直连 PG 热路径预扣-结算，PG 是两个面共同的唯一有状态依赖。
- **金额用 int64 微单位**（ADR-0013）：整数运算，末尾一次四舍五入，杜绝浮点漂移。
- **预扣估算 completion-only 上限**（ADR-0013）：Pre 在 dispatch 之前跑，命中供应商未知且无 tokenizer，故用 `effectiveMaxTokens × 候选最大 completion 费率`；prompt 成本在 Post 按精确用量结算。
- **用量记录 fail-open**（ADR-0016）：钱已在 Post 同步结算，审计行异步落库，缓冲满则丢弃并计数，绝不阻塞请求路径。
- **配置装配纯函数 + 原子换入**（step 8）：`BuildDispatcher(dyn)` 纯函数，watcher 按版本重建、原子替换、失败保留 last-good，路由器每请求经 provider 解析当前调度器。
- **统一 `adapter.Options` + 注册表**（step 8）：组合根泛型构造任意 adapter，零 per-adapter 耦合。
- **会话亲和用 HRW 而非环哈希**（ADR-0018）：`session_affinity` 用 Rendezvous(HRW) 产出确定性全排序，同会话粘同供应商（含 failover 顺序），增删供应商仅 ~1/n 重排；纯函数无状态，多实例天然一致、无需共享存储；与故障转移/熔断正交。

---

## 5. 质量与验证

### 5.1 测试分层
- 单元测试：默认 `go test ./...`（快、无外部依赖）。
- DB 测试：`make test-db`（`dbtest` tag，embedded-postgres，各包独立 RuntimePath 并行安全）。
- 端到端：`make test-e2e`（`e2e` tag，mock 上游，无需真实凭据）。

### 5.2 数据面 e2e 覆盖（22 个用例，全绿）
可复用 bootstrap harness（`test/e2e/harness_test.go`）**忠实镜像 `cmd/gateway` 接线**（embedded PG → admin 面 → bootstrap 超管 → poller → app.OpenStores → auth → billing → DispatcherWatcher → router），并提供测试数据准备（经真实 admin API 建 provider/model/route，经 SQL 建带 allowed_models/过期的 key + 配额），`SyncConfig()` 确定性等待配置生效。

| 类别 | 用例 |
|---|---|
| 模型路由 | priority 命中首选、weighted 双命中、retryable 500 跨供应商 failover |
| 会话亲和 | 同 session 粘同供应商、不同 session 分散、跨请求确定性（ADR-0018） |
| 限流 | 租户 RPM 超限 → 429、限流按租户隔离（ADR-0008） |
| 权限 | allowed_models 允许/拒绝(403)、未知 key(401)、过期 key(401)、配额耗尽(402)、无 max_tokens 仍走配额兜底(402) |
| API 兼容性 | 非流式结构、流式 SSE 拼接+尾部 usage+[DONE]+配额结算、错误信封 |
| 闭环 | 全栈鉴权→计费→路由→转发→结算→异步用量落库 |

### 5.3 并发安全
`billing`（异步 recorder）、`proxy`（调度器热换）、`config`（原子替换）均 `-race` 干净。

---

## 6. 已知缺口与 phase-2 项

| # | 缺口 | 影响 | 优先级 |
|---|---|---|---|
| 1 | cache（响应缓存）插件未接入 gateway | 缓存能力未生效（billing/ratelimit 已接入） | 低（opt-in，代码就绪） |
| 2 | `/v1/models`、`/v1/completions`、embeddings 未实现 | 客户端无法枚举模型/用其余端点 | 低 |
| 3 | 熔断/限流为单实例内存态 | 多实例部署健康/限流态不共享（需 Redis，ADR-0008） | 中（多实例上线前） |
| 4 | TS SDK 契约测试仍为 TODO | SDK 兼容性未自动化验证 | 低 |

> 已闭合：全局 max-tokens 配额兜底（`67c01ae`）、ratelimit 接入 gateway（`1188090`）、
> `request_logs`/OTel 的 `model_resolved`/`fallback` 语义字段填充（ADR-0021，`Dispatcher.Forward`/
> `ForwardStream` 现返回 `DispatchResult`，见 `internal/proxy/dispatcher.go`）、
> `request_logs` 读 API 上线（`GET /api/v1/request-logs`，仿 ADR-0019 usage/audit 模式，
> 见 `internal/store/requestlog_query.go` + `internal/admin/requestlog_handlers.go`；
> ADR-0021 §7 已从"待补"改为"已交付"）。

---

## 7. 结论

数据面**核心请求链路开发完成，且已具备完整的端到端测试 bootstrap 与覆盖**：OpenAI 兼容 API（含流式）、多供应商路由/故障转移、**会话亲和路由（同会话粘同供应商，提升缓存命中/降本）**、密钥鉴权 + 按模型授权、**RPM/TPM 限流**、配额预扣-结算（含全局 max-tokens 兜底）、异步用量审计、配置热更新，全部经真实 PG 持久化并有 22 个 e2e 用例保障，并发安全经 `-race` 确认。企业治理链已齐（限流 → 鉴权 → 授权 → 配额）。剩余项（§6）均为明确记录的、非阻塞的 phase-2 增强，其中「熔断/限流多实例共享」在多实例部署前需引入共享存储（ADR-0008）。
