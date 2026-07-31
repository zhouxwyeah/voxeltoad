# Desktop 个人网关 —— 设计文档

> 本文档沉淀自一轮需求 grill（/grill-me + /grill-with-docs）。目标：把"桌面版个人网关助手"的需求、架构边界、复用映射与实现分期，落到可执行的单一事实来源。
> 前置约束：本工程已存在企业级网关（数据面 `internal/proxy` + 管理面 `internal/admin`），分层见 `design/architecture.md`。

---

## 1. 背景与目标

### 1.1 为什么做
个人开发者日常有多条 LLM 调用源（CodeBuddy / WorkBuddy / Codex / OpenCode / Claude Code 等第三方 Agent，以及自己的脚本）。桌面版定位为以**分发 + 统计**为核心的轻量本地网关：
- **统一分发**：把多个供应商收敛到一个稳定的本地入口，完成模型别名映射、策略选路、协议端点选择与 failover。
- **看清运行态**：以客户端请求为统计单位，优先展示成功率、错误、延迟/TTFT、token、供应商/模型分布与故障转移；成本仅作次级估算指标。
- **解释单次调用**：从请求日志下钻到有序分发路径、Session Trace 和运行日志，回答“流量去了哪里、为什么切换、最终发生了什么”。
- **保留本地分析价值**：完整 prompt/completion 浏览、复制与收藏仍是重要能力，但不再是产品唯一主轴。

产品完善顺序固定为：**可用性 → 可解释性 → 整理能力**。先保证首次配置和真实调用打通，再完善首页与分发分析，最后补 Session 收藏和数据清理体验。

### 1.2 它不是什么（明确排除）
- 不是企业级多租户 / 计费 / 配额强一致系统。
- 不需要认证授权体系（RBAC / operator / 多租户管理面）。
- 不需要节点注册、配置版本历史、跨实例一致性等企业特性。
- 不做按 Agent 的独立治理策略、按价格/延迟自动调权或其他复杂规则引擎。
- 不主动定时调用供应商探活；健康状态来自真实流量和熔断状态的被动投影，避免额外费用与常驻开销。
- 不做"机器学习式自动归纳提示词范式"；只做人工查看、检索、复制、收藏与回放。

### 1.3 关键形态决策
- **流量网关视角（非 SDK 视角）**：桌面网关作为本地代理（`127.0.0.1:port`），第三方 Agent 无法改代码、但都能改 `base_url`，统一指向本网关；网关转发给真实供应商并被动录制。这是唯一零侵入方式。
- **本地优先、单入口安装**：无需注册、登录、云服务、外部数据库或独立可观测基础设施；配置与观测数据只在本机。
- **同仓新增 `cmd/desktop` 组合根**：不 fork、不抽独立 module。企业版与桌面版的差异保持正交，收敛在组合根、SQLite 与桌面 API；ADR-0051 允许共享 Dispatcher 增加一个不参与决策、fail-open 的可选观测契约，用于记录分发步骤。

---

## 2. 范围与边界

| 维度 | 桌面版 | 企业版对应 | 关系 |
|---|---|---|---|
| 路由 / 模型 / 提供商 / failover | 复用 | `internal/proxy` + `internal/adapter` | **原样 import，零差异** |
| 请求录制（元数据） | 复用 + SQLite sink | `request_logs` (ADR-0021) | 复用 `RequestLogSink` 接口；客户端请求是统计单位 |
| 分发路径（目标） | 共享可选观测契约 + SQLite sink | Dispatcher (ADR-0011 / 0051) | **待落地**；有序记录候选跳过与真实上游尝试，不改变路由决策 |
| Trace 捕获（消息/原始体） | 复用 + SQLite sink | `trace_payloads` (ADR-0039) | 已有正文开关与定时留存；主动清理待落地 |
| Session 聚合 | 复用 + 桌面元数据 | ADR-0018 / 0040 / 0051 | 自动只读聚合已落地；收藏与整会话留存豁免待落地 |
| 供应商适配 | 复用 | `internal/adapter/*` | 原样 |
| 治理插件（缓存/提示词注入等） | 按需复用 | `internal/plugin/*` | 原样，默认关 |
| 凭证加密 | 简化 | ADR-0031 AES-256-GCM | 桌面本地单用户，密钥存本地文件/Keychain，可简化 |
| API Key 鉴权 | **K1 种子默认 key** | `auth.Authenticator` + `KeyStore` | 复用结构，种子 1 个不过期、空 `AllowedModels` 的 key |
| 配置来源 | **本地 YAML** | 轮询 admin 快照 | 桌面用 local YAML 喂 `config.Dynamic` 闭包 |
| 存储引擎 | **SQLite** | PostgreSQL | 补 SQLite 实现的 sink/store |
| 桌面 UI | **Wails 壳 + trace 查看器** | `web/` React 控制台 | 复用 `web/` 组件形态，但端点自管 |
| 管理面 / RBAC / operator / billing | **不构建** | `internal/admin` 全套 | 结构性缺席（proxy 不 import admin） |

**正交性论证**：`internal/proxy` 不 import `internal/admin`（已验证）；`internal/authz` 仅被 `internal/admin` 使用（已验证）。因此桌面不构建 admin 即**自动无 RBAC 检查**。数据面唯一的权限闸门是 `modelAllowed`（`internal/proxy/auth_middleware.go:42`），空 `AllowedModels` = 全部放行——"默认租户全权限"在数据面是**结构性成立**，无需实现 RBAC。

### 2.1 与 gateway / admin 的关系（跨模块契约）

桌面网关是 `cmd/gateway` 的**功能子集**（只数据面），且不构建 `cmd/admin`（管理面）。完整的三入口依赖矩阵与共享契约面见 `design/architecture.md` §三入口依赖矩阵。要点：

- **desktop ⊂ gateway（数据面）**：desktop 的 `internal/` 依赖是 gateway 的严格子集（少 `plugin/ratelimit`，因个人单用户无多租户公平诉求）。`proxy.Router` + 全套 `With*` 选项原样复用。
- **desktop ∩ admin = ∅（无管理面）**：desktop 不 import `internal/admin` / `internal/authz`。RBAC、operator、billing、配额强一致等企业特性结构性缺席。
- **共享契约面（变更这些 = 同步影响 desktop）**：
  - `internal/auth.KeyStore` / `auth.Authenticator` —— desktop 实现: `internal/desktopstore/keystore.go`（SQLite），gateway 实现: `internal/store/key.go`（PG）
  - `internal/observability.RequestLogSink` / `TracePayloadSink` —— desktop 实现: `internal/desktopstore/{requestlog,tracepayload}_sink.go`
  - `internal/config.Dynamic` + `GatewaySettings` —— desktop 用本地 YAML 闭包喂, gateway 用 admin 快照轮询喂, **同一批消费者**（`app.NewDispatcherWatcher` / `proxy.WithSettingsSource`）
  - `internal/config/schema.go`（Provider/Model/Route/GatewaySettings 结构体）
- **编译期 canary**：`cmd/desktop/` 无 build tag，`make test` 每 PR 编译它。任何共享接口签名变更会在 desktop 实现上首先撞编译失败——desktop 是共享契约的**编译期守卫**。完整"组装后真能跑通"由 `internal/desktopapp/wiring_test.go`（见 §11）保住。

---

## 3. 架构总览

```
cmd/desktop/
  ├─ main.go                      # 薄入口，调用 desktopapp.Main
  ├─ config/load.go               # 本地 YAML ↔ config.Dynamic
  └─ seed/seed.go                 # 首次启动默认 key + provider/model/route 模板

internal/desktopapp/              # 实际组合与生命周期
  ├─ app.go                       # SQLite → config → proxy → sinks → desktop API
  ├─ run_cli.go / run_desktop.go  # CLI 与 Wails 两种运行形态
  ├─ retention.go                 # 本地留存任务
  └─ wiring_test.go               # 完整桌面链路运行期 canary

internal/desktopstore/            # SQLite 实现（不依赖 internal/store 的 PG 代码）
  ├─ sqlite.go / keystore.go
  ├─ requestlog_sink.go / tracepayload_sink.go
  └─ query.go / prompt.go / retention.go

internal/desktopapi/              # 本地读 API、配置 CRUD 与桌面操作端点
desktop-ui/                       # React + Vite SPA（顶层，与 web/ 并列）
deploy/desktop/                   # 已落地 Wails v2 原生壳与打包配置

复用：
  internal/proxy  internal/adapter  internal/plugin  internal/observability
  internal/auth (Authenticator/KeyRecord)  internal/config (schema + Dynamic)

共享核心例外（ADR-0051）：`internal/proxy` 可增加通用、可选、fail-open 的分发步骤观测契约；
它只发布既有路由决定，不允许桌面策略反向进入 Dispatcher。
```

装配要点（来自探查）：
- `app.NewDispatcherWatcher` 接收 `func() *config.Dynamic`；`billing.NewPlugin`、`proxy.WithSettingsSource` 同理。桌面版直接 `yaml.Unmarshal` 本地 YAML 到 `config.Dynamic` 并传入这些闭包，**核心零改**。
- `Bootstrap.Validate` 对空快照要求 `internal_token_ref`（`config.go:179`）——桌面启动设 `GATEWAY_ALLOW_INSECURE_DEV=1` 跳过该校验（开发态已有此开关）。
- 不轮询 admin 快照，故 `config.Store.set`（未导出）无需使用，桌面绕开 `Store` 直接提供闭包。

---

## 4. 领域模型（Domain Model）

领域按**配置、分发、观测**三个限界上下文拆分（ADR-0051），避免把企业治理概念带入桌面。

### 4.1 配置上下文

```
Provider (聚合根)
  └─ Endpoint[]: ID / Adapter / BaseURL

ModelAlias
  └─ UpstreamMapping[]: Provider / UpstreamModel / Pricing / DefaultMaxTokens

Route: ModelAlias / Strategy / Candidate[]
SetupReadiness (读模型): Provider → Model → Route → Test 四步完成状态
APIKey: 本地客户端鉴权，仅种子 1 个默认 key
```

`ProviderEndpoint` 是 ADR-0049 后的真实契约；桌面 UI 不得再提交旧的顶层 `adapter/base_url`。首次启动通过四步引导完成一条真实可用链路，而不是要求用户自行理解配置依赖。

### 4.2 分发上下文

```
GatewayRequest (聚合根；一次客户端可见的 LLM 调用)
  ├─ RequestIdentity: RequestID / ClientRequestID / TraceID / SessionID
  ├─ Input: IngressProtocol / ModelRequested / Stream / AgentType
  ├─ DispatchStep[] (按 Ordinal 排序，完成选择后成为不可变历史)
  │   ├─ skipped: 候选被熔断等原因跳过，未发起网络调用
  │   └─ attempted: 真实上游调用
  │       └─ SelectionOutcome: selected | retryable_failure | terminal_failure
  └─ RequestOutcome: ServingStepOrdinal / DeliveryOutcome / 最终 Provider/Endpoint/Model /
                     Usage / TTFT / Duration / ErrorType / Fallback / RetryCount /
                     EstimatedCost
```

**统计口径硬约束**：
- 请求量与请求成功率以 `GatewayRequest` 为分母；failover 后成功仍是成功请求。
- `UpstreamAttempt` 仅指 `DispatchStep.action=attempted`；候选跳过不计入尝试数。
- 首次尝试成功率以第一个真实 UpstreamAttempt 为准；熔断跳过不算尝试。
- 精确的 actual failover rate 从 DispatchStep 推导：真实尝试可重试失败后又发起后续真实尝试。现有 `request_logs.fallback` 是按 breaker 过滤后候选索引 `i > 0` 写入的兼容粗信号，可能混入无 forwarder/配置不匹配等非网络跳过，也不能稳定表达熔断过滤与耗尽路径，不作为新统计事实源。
- 首选候选在迭代前被熔断过滤属于“首选绕过”，另计首选候选命中率。
- 流式 `selected` 只表示已锁定 serving step，不代表流最终成功；最终 `completed/stream_error/client_cancelled/error` 记录在 RequestOutcome。
- 尝试失败率、actual failover rate 与首选候选命中率是分发质量指标，不替代客户端最终成功率。
- 历史步骤保存当时的 Provider/Endpoint/原因，不用当前 Route 配置反推旧路径。

### 4.3 观测上下文

```
RequestRecord         # GatewayRequest 的 SQLite 查询投影
OverviewProjection    # 成功率/错误/延迟/TTFT/token/分布/估算成本
PassiveProviderHealth # 近期真实流量 + DispatchStep + 当前熔断状态

SessionProjection (只读聚合)
  ├─ SessionID / SessionSource / AgentType
  ├─ GatewayRequest[]
  └─ SessionFavorite (独立本地元数据；存在时豁免普通留存)

TracePayload
  ├─ Messages (归一化 adapter.Message[]，JSON)
  ├─ RequestRaw / ResponseRaw(SSE 文本) / ErrorRaw
  └─ Summary: StatusCode / StopReason / NMessages / NToolUse

RuntimeLog             # 进程、配置、SQLite 与连接诊断；不存 prompt/completion
RetentionPolicy        # 普通保留期 + 收藏豁免 + 主动删除命令
```

Session 由 session key 自动聚合，用户不能手工合并/拆分；收藏是作用于 SessionID 的本地元数据。被收藏 Session 的请求日志、DispatchStep 与 Trace 一起保留，取消收藏后恢复普通留存。

边界：数据面通过可选观测契约发布 Request/DispatchStep，桌面 store 负责 SQLite 投影，桌面 API/UI 只做查询与本地整理。企业版的"钱路径"（billing 配额强一致）完全移除；桌面成本只是基于本地 Pricing 与真实 Usage 的估算值。

---

## 5. 复用与差异映射（文件级）

| 能力 | 复用/新增 | 文件 | 说明 |
|---|---|---|---|
| proxy 编排 | 复用 + 可选观测扩展 | `internal/proxy/*` | 路由语义不变；ADR-0051 允许发布 DispatchStep |
| adapter | 复用 | `internal/adapter/*` | 零改 |
| plugin | 复用 | `internal/plugin/*` | 零改（默认关） |
| 录制结构 | 复用 | `internal/observability/{requestlog,tracepayload}.go` | Sink 接口已抽象 |
| 鉴权结构 | 复用 | `internal/auth/{auth,apikey}.go` | `KeyRecord`/`Authenticator`/`KeyStore` 接口复用 |
| 配置 schema | 复用 | `internal/config/schema.go` | Provider/Model/Route/GatewaySettings 直接复用 |
| RequestLogSink (PG) | **新增 SQLite** | `internal/desktopstore/requestlog_sink.go` | 对应 `internal/store/requestlog.go` 的原生 SQL |
| DispatchStep observer/sink | **共享契约 + SQLite 实现（待落地）** | `internal/proxy` + `internal/desktopstore` | 只观测既有选择/重试/failover，不参与决策（ADR-0051） |
| TracePayloadSink (PG) | **新增 SQLite** | `internal/desktopstore/tracepayload_sink.go` | 对应 `internal/store/tracepayload.go` |
| KeyStore (PG) | **新增 SQLite** | `internal/desktopstore/keystore.go` | 对应 `internal/store/key.go` |
| 读查询 | **新增** | `internal/desktopstore/query.go` | 复用 `requestlog_query.go`/`tracepayload_query.go` 的查询语义（SQL 需重写，PG 占位符 `$1`/JSONB 不兼容） |
| 配置加载 | **新增** | `cmd/desktop/config/load.go` | 替代 `internal/config/poller.go` 的 admin 轮询 |
| UI 读 API | **新增** | `internal/desktopapi/`（轻量子包） | 替代 `internal/admin` 的 `/request-logs` `/trace/*` 端点 |
| 桌面壳 | **新增** | `desktop-ui/`（顶层，与 `web/` 并列）+ `deploy/desktop/`（Wails 打包） | Wails 桥 + React 前端（复用 `web/` 组件形态） |
| 种子 | **新增** | `cmd/desktop/seed/` | 默认 key + 默认 providers/routes |

**结论**：桌面业务差异仍收敛在 `internal/desktopstore` / `internal/desktopapi` / `cmd/desktop` / `desktop-ui`。唯一允许的共享核心增量是 ADR-0051 的通用分发步骤观测契约；它必须可选、fail-open、不能影响 Dispatcher 决策，企业版无需在同一阶段落库。

---

## 6. 存储设计（SQLite）

### 6.1 三接口实现
- `observability.RequestLogSink` (`internal/observability/requestlog.go:82`) → `internal/desktopstore/requestlog_sink.go`
- `observability.TracePayloadSink` (`internal/observability/tracepayload.go:80`) → `internal/desktopstore/tracepayload_sink.go`
- `auth.KeyStore` (`auth.go:42`) → `internal/desktopstore/keystore.go`

### 6.2 Schema 映射（PG → SQLite）
| PG | SQLite |
|---|---|
| `PARTITION BY RANGE(created_at)` | 去掉；个人量级无需分区 |
| `BIGINT GENERATED BY DEFAULT AS IDENTITY` | `INTEGER PRIMARY KEY AUTOINCREMENT` |
| `JSONB` (messages/request_raw/allowed_models) | `TEXT` 存 JSON 串（`jsonBody()` 归一化逻辑照搬） |
| `TEXT` (response_raw/error_raw) | `TEXT` |
| `TIMESTAMPTZ` | `DATETIME` / `INTEGER`(unix) |
| `$1` 占位符 | `?` 占位符 |

### 6.3 建表 SQL（当前 4 张业务表，目标新增 2 张）

**当前已落地：**
- `request_logs`：已持久化 tenant/group/api_key_id/provider/model_*/tokens/ttft/duration/error_type/blocked_by/fallback/cache_*/request_id/client_request_id/session_id/trace_id/upstream_request_id/session_source/agent_type/ingress_protocol/provider_endpoint/created_at。
- `trace_payloads`：已持久化关联 id 组 + summary(status_code/stop_reason/n_messages/n_tool_use) + messages/request_raw(JSON TEXT)/response_raw/error_raw(TEXT) + ingress_protocol/provider_endpoint。
- `api_keys`：字段对齐 `migrations/00001_initial_schema.sql:77` —— key_id/hash(CHAR64)/tenant_id/group_id/expires_at/allowed_models(TEXT JSON)/revoked_at。桌面仅 1 行种子。
- `prompt_templates`（§10.3 Prompt 收藏，已落地）：title/content(TEXT)/tags(JSON TEXT)/session_id/source_trace_row_id/note/timestamps。

**ADR-0051 目标增量（待落地）：**
- `request_logs` 增加 nullable `serving_step_ordinal` 与 `delivery_outcome`（`completed` / `stream_error` / `client_cancelled` / `request_error`），让流式最终结果可在重启后与 serving DispatchStep 关联。
- `dispatch_steps`：request_log_id/ordinal/provider/endpoint/action/skip_reason/selection_outcome/retryable/status_code/error_type/upstream_request_id/started_at/selection_duration_ms；`UNIQUE(request_log_id, ordinal)`。关联 `request_logs.id` 而非非唯一的 `request_id`；流式 `selected` 只表示锁定，最终 delivery outcome 归父 RequestOutcome。
- `session_favorites`：session_id(PRIMARY KEY)/created_at；存在即让该 session 的 `request_logs`、`trace_payloads` 与 `dispatch_steps` 豁免普通留存。

索引至少包含：`request_logs(session_id, created_at)`、`request_logs(agent_type, created_at)`、`dispatch_steps(request_log_id, ordinal)`、`dispatch_steps(provider, started_at)`。

### 6.4 留存
当前已落地默认 30 天、启动即跑 + 每 24h 的定时 `DELETE`，无需 partition-DROP。`internal/desktopstore/retention.go` 提供 `DeleteRequestLogsBefore`/`DeleteTracePayloadsBefore` + `wal_checkpoint(TRUNCATE)`；两表使用同一窗口，失败仅告警。

ADR-0051 的目标语义：
- 收藏 Session 后，其 request/trace/dispatch steps 整体豁免普通留存；取消收藏后恢复普通策略。
- 提供按 Session 删除、按时间立即清理、清空全部观测记录三类命令；配置、API key 与供应商凭证不随观测记录清空。
- `dispatch_steps` 与其父请求同生命周期删除，不允许留下孤立分发路径。

### 6.5 文件位置（已落地）
默认数据目录为 `~/.voxeltoad/`（`desktop.yaml` + `desktop.db`），日志在 `~/.voxeltoad/logs/desktop.log`（启动时 >10MB 单代轮转）。`-config`/`-db` flag 与 `DESKTOP_CONFIG`/`DESKTOP_DB` env 显式指定时优先（dev/冒烟脚本依赖）。首次以默认路径启动时自动把 cwd 时代的 `./desktop.yaml`、`./desktop.db{,-wal,-shm}` 迁移过去（目标已存在则不覆盖）。原 cwd 默认值对双击启动的 .app 不可靠（cwd 由 LaunchServices 决定）。

---

## 7. 配置来源（本地 YAML + CRUD 热重载）

配置以本地 YAML 文件(`desktop.yaml`)为单一事实来源。**读路径**:`config.Load(path)` 返回一个闭包,该闭包**每次调用都重读文件**(成本仅在 `DispatcherWatcher.rebuild` 时支付——启动、写后重载、Watch 轮询);闭包喂给 `app.NewDispatcherWatcher` / `proxy.WithSettingsSource`,核心包不感知来源。

**写路径(CRUD + 热重载)**:`internal/desktopapi` 暴露 `/api/v1/{providers,models,routes}` 的 CRUD(形状对齐 admin,但操作 YAML 文件而非 PG):
1. 读 YAML → 修改内存 `config.Dynamic`(校验:provider 名唯一、model/route 引用的 provider 存在)
2. `config.SaveFile(path, dyn, gateway)` —— 原子写(temp file + rename),**保留 `gateway:` 引导段**(addr/session_headers 不在 `config.Dynamic` 内;早期版本直接丢该段,重启后静默回退 :8080,已修复)
3. bump `dyn.Version`(让 watcher 看到变化)
4. `watcher.Build()` —— atomic swap dispatcher,**无需重启**
5. rebuild 失败 → 配置已落盘,dispatcher 保留 last-good,API 返回 200 + warning

引用校验:删 provider 时若被 model/route 引用 → 409;创建 model/route 时校验上游 provider 存在 → 400。

- 直接复用 `internal/config/schema.go` 结构体(`Provider`/`Model`/`Route`/`GatewaySettings`)。
- 启动设 `GATEWAY_ALLOW_INSECURE_DEV=1` 跳过 `Bootstrap.Validate` 对空 snapshot 的 `internal_token_ref` 校验。
- 首次运行若无 YAML，由 `cmd/desktop/seed/` 写一份默认配置（含 1 个默认客户端 key + 深度求索/TokenHub/Kimi-code/GLM 四家供应商模板）；未提供对应环境变量时上游 key 为空，因此模板不是开箱即连通。
- UI 在「供应商」「模型」「路由」三页提供 CRUD 表单(见 §10.3)。

---

## 8. 鉴权（K1：种子默认 key）

- 种子 1 个 API key：`KeyRecord{ KeyID:"default", Tenant:"default", Group:"default", Hash:sha256(key), ExpiresAt:nil, AllowedModels:[] }`。
- 第三方 Agent 配置：`base_url=http://127.0.0.1:<port>/v1`，`Authorization: Bearer <默认key>`。
- `authMiddleware`（`internal/proxy/auth_middleware.go:59`）真跑真通过；`modelAllowed` 因空 `AllowedModels` 全部放行。
- **proxy 零改动**。Agent 身份靠 `AgentType` 探测（已内建，覆盖 codebuddy/codex/opencode/workbuddy/claude-code）。

> 备选 K2（未选）：加 `auth.disabled` passthrough 模式，Agent 免填 key。本次选 K1 以保持核心纯复用。

---

## 9. Session 聚合、分发路径与 Trace

- **Session 三级链**（ADR-0018）：`X-Voxeltoad-Session` 头（桌面可配置候选头名，覆盖各 Agent 框架）> body `prompt_cache_key`/`user` > 前缀哈希回退。
- **AgentType 探测**：`RequestLog.AgentType` / `TracePayload.AgentType` 已内建，UI 可按 Agent 过滤；AgentType 只用于观测，不进入路由治理。
- **SessionSource 可观测**：可知一条记录是靠显式头还是前缀哈希聚的（模糊聚类的坑可诊断，非黑盒）。
- **SessionFavorite（待落地）**：Session 仍是只读投影，不支持手工合并/拆分；收藏提供置顶与整会话留存豁免。
- **分发路径（待落地，ADR-0051）**：Request 下展示有序 DispatchStep；区分“候选跳过”和“真实上游尝试”，并记录切换原因、耗时、状态与可用的 upstream request ID。
- **四层 Trace**（ADR-0039）：Session → Request → Messages → Raw，展示完整 messages/request_raw/response_raw/error_raw，支持复制与 Prompt 收藏。桌面单用户本地场景默认开启正文采集，但必须显式显示敏感性、保留期和关闭/清理入口；该默认值不改变企业版默认关闭策略。

---

## 10. 桌面 UI（Vite SPA + Trace 查看器）

### 10.1 技术选型
- **CLI / 开发模式(`make desktop-web-dev`)**:前端是独立的 Vite + React SPA(顶层 `desktop-ui/`,与 `web/` 并列),由 Go 网关用 `http.FileServer` 同源服务。前端 `fetch` 走相对路径 `/api/v1/*`,生产同源、dev 模式靠 Vite `server.proxy`(见 `desktop-ui/vite.config.ts`)转到网关端口。`cmd/desktop/main.go` 始终是无 build tag 的薄入口；真正的分叉位于 `internal/desktopapp/run_cli.go`（`!desktop`）。
- **Wails 打包(已落地,`deploy/desktop/`)**:用 Wails v2 把 Go binary + SPA 包成原生安装包,双平台产物:**macOS `.app`**(darwin/universal)+ **Windows NSIS `.exe`**(amd64,ADR-0043)。打包层 `deploy/desktop/`:`wails.json` + `assets.go`(`//go:embed all:dist`)+ `desktop.go`(app context:原生菜单仅 macOS 挂载——Windows/Linux 菜单栏渲染在窗口内与 SPA 布局不协调,对应动作收进侧边栏底部按钮[重载配置 Ctrl+R/打开配置位置/退出应用]+ `POST /api/v1/app/quit` → `RequestQuit` 走 OnShutdown 优雅停服;关闭按钮 macOS 隐藏到 dock、Win/Linux 直接退出——`HideWindowOnClose` 平台化,`OnBeforeClose` 不否决退出,避免残留隐藏进程占用端口)+ `build/darwin/Info.plist` + `build/windows/{info.json,icon.ico}`。构建脚本 `scripts/build-desktop.sh` 参数化 TARGET(`darwin`|`windows`|`windows-cross`),Makefile 暴露 `desktop-build` / `desktop-build-windows` / `desktop-build-windows-cross` 三个目标;Windows .exe 有两条构建路径——(A)WSL2/Linux 交叉编译(`apt install mingw-w64 nsis` 后跑 `make desktop-build-windows-cross`,开发者推荐)和(B)Windows 原生(`choco install nsis` 后跑 `make desktop-build-windows`);CI 在 push-to-main 时跑 `desktop-windows-build` job 产出 `.exe` artifact(ADR-0043 supersede ADR-0042 §3 的窄面)。**数据面必须保留独立 `net/http.Server`**:第三方 Agent 用 `base_url` 打 `/v1/*` 且依赖 `WriteTimeout:0` 的 SSE 流式,不可走 Wails AssetServer;Wails webview 里的 SPA 通过 AssetServer.Handler 反向代理打到本地 HTTP server 的 `/api/v1/*` + `/v1/*`。`internal/desktopapp/run_desktop.go`（`desktop`）与 `run_cli.go`（`!desktop`）复用同一 `desktopapp` 装配链。
- 前端**复用 `web/` 的 TSX 组件形态**，但端点自管（桌面不构建 admin），用自写的薄客户端 `desktop-ui/src/lib/api.ts`。

### 10.2 API 端点(读 + 配置 CRUD)
`internal/desktopapi/server.go` 暴露:

**录制读 API**(SQLite 查询,语义对齐 admin 的同形状端点):
- `GET /api/v1/request-logs` —— 按时间窗/agent/session 过滤
- `GET /api/v1/sessions` —— 会话聚合列表
- `GET /api/v1/overview` —— 各 Agent 调用量/token/延迟汇总
- `GET /api/v1/trace/sessions/{session_id}` —— session 下请求列表
- `GET /api/v1/trace/rows/{id}` —— 单条 trace 完整 messages/raw(ADR-0040)
- `GET /api/v1/trace/requests/{request_id...}` —— 按 request_id 查 trace(多段通配,因 request_id 含 `/`)

**ADR-0051 目标端点（待落地）**：
- `GET /api/v1/request-logs/{id}/dispatch-steps` —— 按请求日志行主键读取有序分发路径
- `PUT/DELETE /api/v1/session-favorites/{session_id...}` —— 收藏/取消收藏 Session
- `DELETE /api/v1/sessions/{session_id...}` —— 删除单个 Session 的 request/trace/dispatch steps
- `POST /api/v1/observation/purge` —— 按 before 时间立即清理或清空全部观测记录，保留配置

> SetupReadiness 不再需要后端端点：前端组合 listProviders/listModels/listRoutes 计算四步完成度，Test 步骤是 CTA 跳转 Playground 而非追踪状态。

**配置 CRUD**(YAML 文件后端,见 §7):
- `GET/POST /api/v1/providers` + `GET/PUT/DELETE /api/v1/providers/{name}`
- `GET/POST /api/v1/models` + `GET/PUT/DELETE /api/v1/models/{alias}`
- `GET/POST /api/v1/routes` + `GET/PUT/DELETE /api/v1/routes/{alias}`
- `POST /api/v1/config/reload` —— 手动强制重读 + rebuild(兜底)
- `POST /api/v1/config/reveal` —— 在系统文件管理器中定位配置文件(侧边栏底部按钮;`RevealConfigFile` 按 `runtime.GOOS` 分支 Finder/Explorer/xdg-open,macOS 菜单项共用)
- `POST /api/v1/app/quit` —— 退出应用(侧边栏底部按钮;先应答再经 `SetQuitFunc` 注入的 Wails Quit 走正常 OnShutdown 优雅停服)
- `GET/PUT /api/v1/settings` —— 网关级设置(gateway.addr/session_headers 重启生效;trace 三项保存即热生效)

**运行与工具端点**:
- `GET /api/v1/logs?tail=N` —— 进程日志环形缓冲(运行日志页;stdlib + access log 经 `observability.SetLogOutput` tee 进 ring + 文件)
- `GET /api/v1/apikey` / `POST /api/v1/apikey/rotate` —— 默认密钥查看/轮换(明文仅内存态可知,轮换即返回一次)
- `POST /api/v1/playground/chat` —— 进程内走完整数据面链路的小请求(连通性测试页;不计入请求日志)
- `GET/POST /api/v1/prompts` + `GET/PUT/DELETE /api/v1/prompts/{id}` —— Prompt 收藏（§10.3）

### 10.3 核心页面
1. **概览（部分落地）**：配置向导卡片已落地（Provider → Model → Route → Test 四步进度）；请求级统计已落地（总调用/成功率/平均延迟/平均TTFT/Token + Provider/Model/Agent 分布 + 错误类型分布 + 估算成本）。actual failover rate / 首次尝试成功率 / 首选候选命中率等待 Batch B-2 DispatchStep。
2. **首次配置引导（已落地）**：无可用链路时，概览顶部显示配置向导卡片（Provider → Model → Route → Test 四步进度 + 唯一下一步 CTA）；三步配置完成后卡片隐藏，退化为普通运行态摘要。前端组合 listProviders/listModels/listRoutes，零后端改动。
3. **Session 浏览器（部分落地）**：按 Agent 过滤、SessionSource 展示和 Trace 下钻已落地；收藏、置顶与留存豁免待落地。不支持手工合并/拆分。
4. **请求/分发路径查看器（目标，待落地）**：请求时间线先展示客户端最终结果，再展开有序 DispatchStep，明确候选跳过、真实尝试、切换原因、耗时和上游关联 ID。
5. **Trace 查看器（已落地）**：单 session 内请求时间线；点开看完整 messages(system/user/assistant/tool_use)、request_raw、response_raw、error_raw；支持复制 prompt。正文采集默认开启但可关闭；敏感性与清理反馈待补。
6. **供应商（当前阻断，Batch A 修复）**：后端已使用 ADR-0049 `endpoints[]`，但当前桌面 UI 仍提交旧顶层 `adapter/base_url`。目标 Modal 必须提交每项 `id/adapter/base_url`；weight/timeouts 不在 UI 暴露，创建时写默认值、编辑时保留原值；明文 key 以 `plain://` 存本地 YAML。
7. **模型**：表格展示 alias + upstreams 行内 pill(provider · 上游模型 · 输入/输出价格 · cache %)；Modal 支持描述、context_length、capabilities、tags 与动态 upstream 行；价格显示美元、提交转 micro，币种为 USD。
8. **路由**：表格展示 model_alias + strategy pill + providers pill；Modal 支持 priority/weighted/round_robin/session_affinity，候选 provider 按所选模型的 upstream 过滤。
9. **收藏/打标签好 prompt（已落地）**：`prompt_templates` 表 + `/prompts` 列表页（搜索/标签筛选/复制/编辑/删除），Trace 查看器可从 messages 预填收藏。Prompt 收藏与 Session 收藏是两个概念。
10. **请求日志（已落地）**：`/request-logs`——多维过滤 + 分页表格；ADR-0051 增量是在行详情中展开分发路径与估算成本。
11. **运行日志（已落地）**：`/logs`——进程日志查看（3s 轮询、tail 档位、客户端关键字过滤），完整历史在 `logs/desktop.log`。
12. **设置（部分落地）**：已有网关监听、Trace 正文采集开关/上限/留存天数和 API key 管理；待补敏感性说明、按时间立即清理与清空全部观测数据。
13. **连通性测试（已落地）**：`/playground`——选模型发小请求，展示响应/耗时/命中供应商/token 用量，上游错误原样展示。

> **UI 对齐原则(2026-07)**：desktop-ui 的布局、表格、表单字段、按钮变体、Modal 结构镜像 admin web(`web/`)，唯一事实来源是 `design/design-system.md` + `web/src`。有意偏差包括：无 tenant/billing/quota 视图、成本仅作本地估算、provider 的 weight/timeouts 隐藏但随提交保留、明文凭证映射 `plain://`、品牌名「桌面网关助手」+ zh-CN 单语言。

### 10.4 Modal 内表单布局规范(desktop-ui)

约束 desktop-ui 中所有编辑/新增 Modal 的表单排版。与 admin web 的 Modal 契约保持一致(design-system.md §3);偏离需在 PR 描述中说明。

**Modal 尺寸选用**(`components/ui/modal.tsx`,与 admin 同四档):
- `sm`(max-w-sm):纯确认
- `md`(max-w-md):简单详情(如路由详情)
- `lg`(max-w-lg):供应商表单
- `xl`(max-w-2xl):含动态行的表单(模型 upstreams、路由 providers)

**表单操作条**:表单底部按钮不使用 Modal 的 `footer` slot,而用 `modalFormActionsClass`(sticky 底部操作条,与 admin 一致),保证长表单滚动时按钮始终可见。

**Field 组件**(`components/ui/field.tsx`):所有表单控件必须包在 `<Field>` 中,由它统一渲染 label/required 星标/hint/error/suffix。禁止再用 `<label className="text-sm">…<Input/></label>` 这种把 label 当 wrapper 的写法。

**动态行布局**:与 admin 同形——模型 upstream 行是 `flex flex-col gap-3 rounded-md border p-3` 卡片(provider+上游模型两列 → 默认 max tokens → 价格三列 → 右下「移除」);路由 provider 行是 `grid grid-cols-[1fr_120px_auto] items-end gap-3`(供应商 + 权重 + 移除)。

**单位表达**:禁止用 placeholder 承担单位说明(反模式,输入后单位消失)。带单位的字段必须用 Field 的 `suffix` 或 `hint`(如缓存命中 % 的「50 = 缓存 token 半价」)。

**枚举字段**:固定少量选项(≤ 5 个)一律用 `Select`,禁止用 Input 自由文本。当前枚举清单:
- `ProviderEndpoint.adapter`：`openai` / `claude`（后端 adapter registry）
- `Provider.type`:品牌预设(openai/tencent/zhipu/anthropic/google/azure/deepseek/bedrock)+ "自定义…" 兜底 Input(与 admin 一致)
- `Provider` 凭证方式:`ref`(API 密钥引用)/ `key`(明文,存 `plain://`)
- `Route.strategy`:`priority` / `weighted` / `round_robin` / `session_affinity`
- 会话过滤器 `agent_type`:见 Sessions.tsx 常量

**Markdown 渲染**:Trace 详情中仅 `role=assistant` 的 text block 用 `react-markdown` 渲染;`thinking`/`tool_result`/`user`/`system` 保持 `<pre>` 纯文本。

---

## 11. 测试策略

正交分离 + 组合根独立测：

**已落地的 Go 测试（无 build tag，随 `make test` / CI 运行）**：
- **核心包测试不受影响**：`internal/proxy` / `internal/adapter` / `internal/observability` 等原有测试照跑（CI 不变）。
- **`internal/desktopstore/query_test.go`**：纯 unit。真 SQLite (`t.TempDir()`) 种子数据,断言 session 聚合 / request-log 列表 / trace 查询正确性。
- **`internal/desktopstore/keystore_test.go`**：种子 key 的 `LookupByHash` 命中/miss/过期、空 `AllowedModels` 全放行。
- **`internal/desktopapi/server_test.go`**：真 SQLite + `httptest` 真读 API 服务端,覆盖 7 个端点 + `%2F` request_id 边界。
- **`internal/desktopapp/wiring_test.go`**（**关联影响守卫**）：in-process 装配完整桌面链路（`proxy.Router` + desktop SQLite sinks + `config.Load` 闭包 + mock 上游）,真打 `/v1/chat/completions`（流式 + 非流式）,断言 `request_logs`/`trace_payloads` 落 SQLite 且读 API 能取回。**这是唯一验证"组装后真能跑通"的测试**——若共享接口签名/语义变更未被编译器抓住,这里会抓住。模式对照 `design/e2e.md` 的 desktop 节。

**已存在但尚未进入每 PR 门禁：**
- **`desktop-ui/src/lib/format.test.ts`**：前端纯函数冒烟（vitest）；当前 `make ci` 不安装或运行 `desktop-ui` 测试。组件渲染/Playwright 继续延后。

**手动测试脚本**：
- `scripts/desktop-test.sh`：build → 后台启动 → curl 真打 → 读 API 验证 → 清理（对照 `scripts/devstack-test.sh` 形态）。
- `scripts/desktop-web-dev.sh`：双进程开发环境（Go 网关 + Vite HMR）,依赖 `desktop-ui/vite.config.ts` 的 `server.proxy` 把 `/api/v1`/`/v1` 转到网关端口。

**配置真实上游 key**：默认 seed 的 provider 模板不含真实 key，调用会被上游拒绝。`GATEWAY_SEED_DEEPSEEK_KEY` / `GATEWAY_SEED_TOKENHUB_KEY` / `GATEWAY_SEED_KIMI_KEY` / `GATEWAY_SEED_GLM_KEY` 仅在**首次生成** `desktop.yaml` 时从进程环境展开；desktop 不自动读取项目 `.env`，配置文件已存在后再设置环境变量也不会回填。Batch A 完成后应通过 Provider UI 配置；当前开发态需在首次启动前导出环境变量或直接编辑本地 `desktop.yaml`。

**显式延后**：Wails 壳的浏览器/原生 UI 自动化测试（当前成本高、收益低，待桌面核心流程稳定后再评估）。

---

## 12. 文件级演进边界

**既有桌面边界：**
- `cmd/desktop/` / `internal/desktopapp/` —— 组合根与运行生命周期
- `internal/desktopstore/` —— SQLite 的 key、request、trace、prompt 与查询实现
- `internal/desktopapi/` —— 无 RBAC 的本地 API
- `desktop-ui/` —— React + Vite 桌面前端

**ADR-0051 允许的共享增量：**
- `internal/proxy`（必要时配合 `internal/observability`）只新增可选 DispatchStep observer/event 契约及发布点。
- 不在共享层加入 SQLite、Session 收藏、桌面 API、首页投影或首次引导逻辑。

**桌面目标增量：**
- `internal/desktopstore/`：`dispatch_steps`、`session_favorites`、收藏感知留存与主动清理。
- `internal/desktopapi/`：setup readiness、分发路径、Session 收藏/删除、观测数据 purge。
- `desktop-ui/`：先修 Provider `endpoints[]` 契约，再做首次引导、运行态首页、分发路径、Session 收藏与清理。

其余 `internal/adapter`、`internal/plugin`、`internal/auth`、`internal/config` 继续原样复用。

---

## 13. 风险与开放问题

1. **前缀哈希回退的 session 抖动**：coding agent 的 system prompt 含动态尾巴时可能拆 session（ADR-0018 §5）。缓解：桌面配置候选 session 头名覆盖各 Agent；UI 显示 `SessionSource` 让用户识别。
2. **SQLite 并发写**：录制走异步 fail-open 路径；DispatchStep 增加写放大。必须批量/异步写并保持请求转发优先，不能为完整分析阻塞客户端。
3. **观测记录可能不完整**：进程崩溃、队列满或 SQLite 错误都可丢 request/step/trace。UI 不把缺失步骤解释为“未发生”，应显示观测完整性状态。
4. **被动健康的滞后性**：无真实流量时不能证明供应商当前可用。首页必须显示“最后观测时间”，不能把久未使用误标为健康。
5. **凭证安全**：默认 key 存本地文件，单用户桌面可接受；若追求更好，接系统 Keychain（后续增强）。
6. **收藏与清理一致性**：SessionFavorite、request、dispatch steps、trace 删除必须保持同一会话语义，避免孤儿记录或收藏失效。

---

## 14. 当前演进批次

基础网关、SQLite、配置 CRUD、请求/Trace/运行日志、Playground 与双平台 Wails 打包已经落地。下一阶段不再按“搭骨架”推进，而按产品成熟度收敛：

### Batch A — 可用性（最高优先级）
- [x] 修复桌面 Provider UI 与 ADR-0049 `endpoints[]` 的阻断性契约漂移。
- [x] 补齐桌面 SQLite 对共享请求字段（ingress_protocol / provider_endpoint）的持久化同步。
- [x] 实现 Provider → Model → Route → Test 四步 SetupReadiness 与首页引导。
- 验收：用户仅通过桌面 UI 能建立一条真实可调用链路，不能依赖手改 YAML。

### Batch B — 可解释性
- [x] 首页改为请求级可靠性与用量总览：最终成功率、错误、延迟/TTFT、token、Provider/Model/Agent 分布、次级估算成本。fallback 率（legacy coarse）已展示，actual failover rate 待 DispatchStep。
- [x] 共享 Dispatcher 发布可选 DispatchStep；桌面 SQLite/API/UI 展示有序分发路径。熔断跳过的 skipped steps 留待后续（需 router 层面改动）。
- [x] 提供被动 ProviderHealth，明确最后观测时间，不主动产生探测请求。
- 验收：任一发生 failover 的请求都能说明"评估了谁、跳过/调用了谁、为何切换、最终谁响应"。

### Batch C — 整理能力
- [x] Session 收藏、置顶与整会话留存豁免。
- [x] 按 Session 删除、按时间立即清理、清空全部观测记录。
- [x] Trace 设置补敏感性、当前保留范围与清理反馈。
- 验收：用户能长期保留少量重要会话，同时安全清理其余本地敏感数据。

> 范围封顶：不做 ML 自动归纳、Agent 专属治理、智能动态调度、主动探活，以及 RBAC/operator/quota/节点注册/配置版本历史等企业特性。
