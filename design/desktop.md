# Desktop 个人网关 —— 设计文档

> 本文档沉淀自一轮需求 grill（/grill-me + /grill-with-docs）。目标：把"桌面版个人网关助手"的需求、架构边界、复用映射与实现分期，落到可执行的单一事实来源。
> 前置约束：本工程已存在企业级网关（数据面 `internal/proxy` + 管理面 `internal/admin`），分层见 `design/architecture.md`。

---

## 1. 背景与目标

### 1.1 为什么做
个人开发者日常有多条 LLM 调用源（CodeBuddy / WorkBuddy / Codex / OpenCode / Claude Code 等第三方 Agent，以及自己的脚本），且想：
- **统一路由**：把多个供应商（OpenAI / Claude / 混元 / 本地 ollama 等）收敛到一个本地入口，按策略选路、failover。
- **看 Agent 行为**：被动录制所有流经的调用，按 Agent / Session 聚类，翻看真实的 prompt / completion，用于**分析与学习提示词写法**。
- **顺手记账**：调用量、token、成本只是附加产物，不是主要驱动力。

### 1.2 它不是什么（明确排除）
- 不是企业级多租户 / 计费 / 配额强一致系统。
- 不需要认证授权体系（RBAC / operator / 多租户管理面）。
- 不需要节点注册、配置版本历史、跨实例一致性等企业特性。
- 不做"机器学习式自动归纳提示词范式"（粒度 3）——只做人工查看 + 复制的录制/回放（粒度 1 + 2）。

### 1.3 关键形态决策
- **流量网关视角（非 SDK 视角）**：桌面网关作为本地代理（`127.0.0.1:port`），第三方 Agent 无法改代码、但都能改 `base_url`，统一指向本网关；网关转发给真实供应商并被动录制。这是唯一零侵入方式。
- **同仓新增 `cmd/desktop` 组合根**：不 fork、不抽独立 module。核心层（proxy/adapter/plugin/observability）只读、随仓前进；企业版与桌面版的差异**正交**，收敛在组合根的存储实现与配置来源上。

---

## 2. 范围与边界

| 维度 | 桌面版 | 企业版对应 | 关系 |
|---|---|---|---|
| 路由 / 模型 / 提供商 / failover | 复用 | `internal/proxy` + `internal/adapter` | **原样 import，零差异** |
| 请求录制（元数据） | 复用 + SQLite sink | `request_logs` (ADR-0021) | 复用 `RequestLogSink` 接口 |
| Trace 捕获（消息/原始体） | 复用 + SQLite sink | `trace_payloads` (ADR-0039) | 复用 `TracePayloadSink` 接口 |
| Session 聚合 | 复用 | ADR-0018 / 0040 | 三级 session key 链 + AgentType 探测 |
| 供应商适配 | 复用 | `internal/adapter/*` | 原样 |
| 治理插件（缓存/提示词注入等） | 按需复用 | `internal/plugin/*` | 原样，默认关 |
| 凭证加密 | 简化 | ADR-0031 AES-256-GCM | 桌面本地单用户，密钥存本地文件/Keychain，可简化 |
| API Key 鉴权 | **K1 种子默认 key** | `auth.Authenticator` + `KeyStore` | 复用结构，种子 1 个不过期、空 `AllowedModels` 的 key |
| 配置来源 | **本地 YAML** | 轮询 admin 快照 | 桌面用 local YAML 喂 `config.Dynamic` 闭包 |
| 存储引擎 | **SQLite** | PostgreSQL | 补 SQLite 实现的 sink/store |
| 桌面 UI | **Wails 壳 + trace 查看器** | `web/` React 控制台 | 复用 `web/` 组件形态，但端点自管 |
| 管理面 / RBAC / operator / billing | **不构建** | `internal/admin` 全套 | 结构性缺席（proxy 不 import admin） |

**正交性论证**：`internal/proxy` 不 import `internal/admin`（已验证）；`internal/authz` 仅被 `internal/admin` 使用（已验证）。因此桌面不构建 admin 即**自动无 RBAC 检查**。数据面唯一的权限闸门是 `modelAllowed`（`auth_middleware.go:29`），空 `AllowedModels` = 全部放行——"默认租户全权限"在数据面是**结构性成立**，无需实现 RBAC。

### 2.5 与 gateway / admin 的关系（跨模块契约）

桌面网关是 `cmd/gateway` 的**功能子集**（只数据面），且不构建 `cmd/admin`（管理面）。完整的三入口依赖矩阵与共享契约面见 `design/architecture.md` §三入口依赖矩阵。要点：

- **desktop ⊂ gateway（数据面）**：desktop 的 `internal/` 依赖是 gateway 的严格子集（少 `plugin/ratelimit`，因个人单用户无多租户公平诉求）。`proxy.Router` + 全套 `With*` 选项原样复用。
- **desktop ∩ admin = ∅（无管理面）**：desktop 不 import `internal/admin` / `internal/authz`。RBAC、operator、billing、配额强一致等企业特性结构性缺席。
- **共享契约面（变更这些 = 同步影响 desktop）**：
  - `internal/auth.KeyStore` / `auth.Authenticator` —— desktop 实现: `internal/desktopstore/keystore.go`（SQLite），gateway 实现: `internal/store/key.go`（PG）
  - `internal/observability.RequestLogSink` / `TracePayloadSink` —— desktop 实现: `internal/desktopstore/{requestlog,tracepayload}_sink.go`
  - `internal/config.Dynamic` + `GatewaySettings` —— desktop 用本地 YAML 闭包喂, gateway 用 admin 快照轮询喂, **同一批消费者**（`app.NewDispatcherWatcher` / `proxy.WithSettingsSource`）
  - `internal/config/schema.go`（Provider/Model/Route/GatewaySettings 结构体）
- **编译期 canary**：`cmd/desktop/` 无 build tag，`make test` 每 PR 编译它。任何共享接口签名变更会在 desktop 实现上首先撞编译失败——desktop 是共享契约的**编译期守卫**。完整"组装后真能跑通"由 `cmd/desktop/wiring_test.go`（见 §11）保住。

---

## 3. 架构总览

```
cmd/desktop/                      # 新增组合根（唯一改动面）
  ├─ main.go                      # 装配：SQLite 打开 → 种子默认 key → 本地 YAML → proxy → sinks → 内嵌 SPA
  ├─ store/                       # 新增：SQLite 实现（不依赖 internal/store 的 PG 代码）
  │   ├─ sqlite.go                # gorm(sqlite) 打开 + 建表（DDL 见 §6）
  │   ├─ keystore.go              # 实现 auth.KeyStore（LookupByHash）
  │   ├─ requestlog_sink.go       # 实现 observability.RequestLogSink
  │   ├─ tracepayload_sink.go     # 实现 observability.TracePayloadSink
  │   └─ query.go                 # 实现 session/request/trace 读查询（供 UI）
  ├─ config/                      # 新增：本地 YAML → config.Dynamic
  │   └─ load.go                  # yaml.Unmarshal 到 config.Dynamic，返回闭包
  └─ seed/                        # 新增：首次启动种子默认 key + 默认 providers/routes

desktop-ui/                       # 新增：桌面个人网关前端（顶层，与 web/ 并列）
                                  # React + Vite，复用 web/ 组件形态；后续 Wails 壳由 deploy/desktop/ 承担

复用（零改动）：
  internal/proxy  internal/adapter  internal/plugin  internal/observability
  internal/auth (Authenticator/KeyRecord)  internal/config (schema + Dynamic)
```

装配要点（来自探查）：
- `app.NewDispatcherWatcher` 接收 `func() *config.Dynamic`；`billing.NewPlugin`、`proxy.WithSettingsSource` 同理。桌面版直接 `yaml.Unmarshal` 本地 YAML 到 `config.Dynamic` 并传入这些闭包，**核心零改**。
- `Bootstrap.Validate` 对空快照要求 `internal_token_ref`（`config.go:179`）——桌面启动设 `GATEWAY_ALLOW_INSECURE_DEV=1` 跳过该校验（开发态已有此开关）。
- 不轮询 admin 快照，故 `config.Store.set`（未导出）无需使用，桌面绕开 `Store` 直接提供闭包。

---

## 4. 领域模型（Domain Model）

```
Agent (探测实体, 非持久化)
  ├─ AgentType: claude-code | codex | codebuddy | workbuddy | opencode | "" (未知)
  └─ 来源: 请求头/UA 探测，已内建于企业版 (RequestLog.AgentType 字段)

Session (聚合根, 来自 ADR-0018 三级链)
  ├─ SessionID: X-Voxeltoad-Session 头 > body(prompt_cache_key/user) > 前缀哈希回退
  ├─ SessionSource: header-config | header-generic | body-* | prefix
  └─ 包含 N 个 Request (按 created_at 排序)

Request (实体, = request_logs 一行)
  ├─ RequestID / TraceID / UpstreamRequestID
  ├─ Provider / ModelRequested / ModelResolved
  ├─ Token(Prompt/Completion/Total) / TTFTms / Durationms
  ├─ ErrorType / BlockedBy / Fallback / CacheHit
  └─ AgentType / SessionID / SessionSource

TracePayload (值对象, = trace_payloads 一行, 1:1 join Request via RequestID)
  ├─ Messages (归一化 adapter.Message[]，JSON)
  ├─ RequestRaw / ResponseRaw(SSE 文本) / ErrorRaw
  └─ Summary: StatusCode / StopReason / NMessages / NToolUse

Provider / Model / Route (配置实体, 复用 schema.go)
APIKey (实体, KeyStore, 仅种子 1 个默认)
```

边界：数据面（录制）↔ 桌面 store（SQLite）↔ 桌面 UI（查询）三者通过接口/`config.Dynamic` 解耦。企业版的"钱路径"（billing 配额强一致）在桌面被完全移除。

---

## 5. 复用与差异映射（文件级）

| 能力 | 复用/新增 | 文件 | 说明 |
|---|---|---|---|
| proxy 编排 | 复用 | `internal/proxy/*` | 零改 |
| adapter | 复用 | `internal/adapter/*` | 零改 |
| plugin | 复用 | `internal/plugin/*` | 零改（默认关） |
| 录制结构 | 复用 | `internal/observability/{requestlog,tracepayload}.go` | Sink 接口已抽象 |
| 鉴权结构 | 复用 | `internal/auth/{auth,apikey}.go` | `KeyRecord`/`Authenticator`/`KeyStore` 接口复用 |
| 配置 schema | 复用 | `internal/config/schema.go` | Provider/Model/Route/GatewaySettings 直接复用 |
| RequestLogSink (PG) | **新增 SQLite** | `internal/desktopstore/requestlog_sink.go` | 对应 `internal/store/requestlog.go` 的原生 SQL |
| TracePayloadSink (PG) | **新增 SQLite** | `internal/desktopstore/tracepayload_sink.go` | 对应 `internal/store/tracepayload.go` |
| KeyStore (PG) | **新增 SQLite** | `internal/desktopstore/keystore.go` | 对应 `internal/store/key.go` |
| 读查询 | **新增** | `internal/desktopstore/query.go` | 复用 `requestlog_query.go`/`tracepayload_query.go` 的查询语义（SQL 需重写，PG 占位符 `$1`/JSONB 不兼容） |
| 配置加载 | **新增** | `cmd/desktop/config/load.go` | 替代 `internal/config/poller.go` 的 admin 轮询 |
| UI 读 API | **新增** | `internal/desktopapi/`（轻量子包） | 替代 `internal/admin` 的 `/request-logs` `/trace/*` 端点 |
| 桌面壳 | **新增** | `desktop-ui/`（顶层，与 `web/` 并列）+ `deploy/desktop/`（Wails 打包） | Wails 桥 + React 前端（复用 `web/` 组件形态） |
| 种子 | **新增** | `cmd/desktop/seed/` | 默认 key + 默认 providers/routes |

**结论**：核心 `internal/` 包**无一需要修改**（除可选地在 `internal/store` 增加 SQLite 打开能力；但若桌面 store 独立实现，则连这都不需要）。改动全部在新增的 `cmd/desktop/` 树内。

---

## 6. 存储设计（SQLite）

### 6.1 三接口实现
- `observability.RequestLogSink` (`requestlog.go:66`) → `internal/desktopstore/requestlog_sink.go`
- `observability.TracePayloadSink` (`tracepayload.go:65`) → `internal/desktopstore/tracepayload_sink.go`
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

### 6.3 建表 SQL（桌面版，4 张表）
- `request_logs`：字段对齐 `RequestLog`（`internal/observability/requestlog.go:15`）—— tenant/group/api_key_id/provider/model_*/tokens/ttft/duration/error_type/blocked_by/fallback/cache_*/request_id/session_id/trace_id/upstream_request_id/session_source/agent_type/created_at。
- `trace_payloads`：字段对齐 `TracePayload`（`tracepayload.go:22`）—— 关联 id 组 + summary(status_code/stop_reason/n_messages/n_tool_use) + messages/request_raw(JSON TEXT)/response_raw/error_raw(TEXT)。
- `api_keys`：字段对齐 `migrations/00001_initial_schema.sql:77` —— key_id/hash(CHAR64)/tenant_id/group_id/expires_at/allowed_models(TEXT JSON)/revoked_at。桌面仅 1 行种子。
- `prompt_templates`（§10.3-7 prompt 收藏，已落地）：title/content(TEXT)/tags(JSON TEXT)/session_id/source_trace_row_id/note/timestamps。
- 索引：`(session_id, created_at)`、`(agent_type, created_at)`（对应企业版 `(tenant, created_at)` 索引语义）。

### 6.4 留存（已落地）
个人量级，简单策略：保留天数（默认 30 天，读热加载 `settings.trace.retention_days`），定时 `DELETE`，无需 partition-DROP。实现：`internal/desktopstore/retention.go` 提供 `DeleteRequestLogsBefore`/`DeleteTracePayloadsBefore` + `wal_checkpoint(TRUNCATE)`；`internal/desktopapp/retention.go` 的 sweeper 启动即跑 + 每 24h，两表同一窗口（保证会话列表与 trace 一致），失败仅告警。

### 6.5 文件位置（已落地）
默认数据目录为 `~/.voxeltoad/`（`desktop.yaml` + `desktop.db`），日志在 `~/.voxeltoad/logs/desktop.log`（启动时 >10MB 单代轮转）。`-config`/`-db` flag 与 `DESKTOP_CONFIG`/`DESKTOP_DB` env 显式指定时优先（dev/冒烟脚本依赖）。首次以默认路径启动时自动把 cwd 时代的 `./desktop.yaml`、`./desktop.db{,-wal,-shm}` 迁移过去（目标已存在则不覆盖）。原 cwd 默认值对双击启动的 .app 不可靠（cwd 由 LaunchServices 决定）。

---

## 7. 配置来源（本地 YAML + CRUD 热重载）

配置以本地 YAML 文件(`desktop.yaml`)为单一事实来源。**读路径**:`config.Load(path)` 返回一个闭包,该闭包**每次调用都重读文件**(成本仅在 `DispatcherWatcher.rebuild` 时支付——启动、写后���重载、Watch 轮询);闭包喂给 `app.NewDispatcherWatcher` / `proxy.WithSettingsSource`,核心包不感知来源。

**写路径(CRUD + 热重载)**:`internal/desktopapi` 暴露 `/api/v1/{providers,models,routes}` 的 CRUD(形状对齐 admin,但操作 YAML 文件而非 PG):
1. 读 YAML → 修改内存 `config.Dynamic`(校验:provider 名唯一、model/route 引用的 provider 存在)
2. `config.SaveFile(path, dyn, gateway)` —— 原子写(temp file + rename),**保留 `gateway:` 引导段**(addr/session_headers 不在 `config.Dynamic` 内;早期版本直接丢该段,重启后静默回退 :8080,已修复)
3. bump `dyn.Version`(让 watcher 看到变化)
4. `watcher.Build()` —— atomic swap dispatcher,**无需重启**
5. rebuild 失败 → 配置已落盘,dispatcher 保留 last-good,API 返回 200 + warning

引用校验:删 provider 时若被 model/route 引用 → 409;创建 model/route 时校验上游 provider 存在 → 400。

- 直接复用 `internal/config/schema.go` 结构体(`Provider`/`Model`/`Route`/`GatewaySettings`)。
- 启动设 `GATEWAY_ALLOW_INSECURE_DEV=1` 跳过 `Bootstrap.Validate` 对空 snapshot 的 `internal_token_ref` 校验。
- 首次运行若无 YAML,由 `cmd/desktop/seed/` 写一份默认(含 1 个默认 key + 深度求索/TokenHub/Kimi-code 三家真实供应商模板)。
- UI 在「供应商」「模型」「路由」三页提供 CRUD 表单(见 §10.3)。

---

## 8. 鉴权（K1：种子默认 key）

- 种子 1 个 API key：`KeyRecord{ KeyID:"default", Tenant:"default", Group:"default", Hash:sha256(key), ExpiresAt:nil, AllowedModels:[] }`。
- 第三方 Agent 配置：`base_url=http://127.0.0.1:<port>/v1`，`Authorization: Bearer <默认key>`。
- `authMiddleware`（`auth_middleware.go:44`）真跑真通过；`modelAllowed` 因空 `AllowedModels` 全部放行。
- **proxy 零改动**。Agent 身份靠 `AgentType` 探测（已内建，覆盖 codebuddy/codex/opencode/workbuddy/claude-code）。

> 备选 K2（未选）：加 `auth.disabled` passthrough 模式，Agent 免填 key。本次选 K1 以保持核心纯复用。

---

## 9. Session 聚合与 Trace（复用企业版能力）

直接复用，桌面版无额外逻辑：
- **Session 三级链**（ADR-0018）：`X-Voxeltoad-Session` 头（桌面可配置候选头名，覆盖各 Agent 框架）> body `prompt_cache_key`/`user` > 前缀哈希回退。你已确认各 Agent 均发 session id，故为干净聚合。
- **AgentType 探测**：`RequestLog.AgentType` / `TracePayload.AgentType` 已内建，UI 可按 Agent 过滤。
- **SessionSource 可观测**：可知一条记录是靠显式头还是前缀哈希聚的（模糊聚类的坑可诊断，非黑盒）。
- **四层 Trace**（ADR-0039）：Session → Request → Messages → Raw，`trace_payloads` 已捕获完整 messages/request_raw/response_raw/error_raw。**"学提示词"的载体即此**——UI 展示每 session 的请求序列与消息内容，支持复制。

---

## 10. 桌面 UI（Vite SPA + Trace 查看器）

### 10.1 技���选型
- **CLI / 开发模式(`make desktop-web-dev`)**:前端是独立的 Vite + React SPA(顶层 `desktop-ui/`,与 `web/` 并列),由 Go 网关用 `http.FileServer` 同源服务。前端 `fetch` 走相对路径 `/api/v1/*`,生产同源、dev 模式靠 Vite `server.proxy`(见 `desktop-ui/vite.config.ts`)转到网关端口。`cmd/desktop/main.go` 走 `//go:build !desktop` 的 `run_cli.go`。
- **Wails 打包(已落地,`deploy/desktop/`)**:用 Wails v2 把 Go binary + SPA 包成原生安装包,双平台产物:**macOS `.app`**(darwin/universal)+ **Windows NSIS `.exe`**(amd64,ADR-0043)。打包层 `deploy/desktop/`:`wails.json` + `assets.go`(`//go:embed all:dist`)+ `desktop.go`(app context:原生菜单仅 macOS 挂载——Windows/Linux 菜单栏渲染在窗口内与 SPA 布局不协调,对应动作收进侧边栏底部按钮[重载配置 Ctrl+R/打开配置位置/退出应用]+ `POST /api/v1/app/quit` → `RequestQuit` 走 OnShutdown 优雅停服;关闭按钮 macOS 隐藏到 dock、Win/Linux 直接退出——`HideWindowOnClose` 平台化,`OnBeforeClose` 不否决退出,避免残留隐藏进程占用端口)+ `build/darwin/Info.plist` + `build/windows/{info.json,icon.ico}`。构建脚本 `scripts/build-desktop.sh` 参数化 TARGET(`darwin`|`windows`|`windows-cross`),Makefile 暴露 `desktop-build` / `desktop-build-windows` / `desktop-build-windows-cross` 三个目标;Windows .exe 有两条构建路径——(A)WSL2/Linux 交叉编译(`apt install mingw-w64 nsis` 后跑 `make desktop-build-windows-cross`,开发者推荐)和(B)Windows 原生(`choco install nsis` 后跑 `make desktop-build-windows`);CI 在 push-to-main 时跑 `desktop-windows-build` job 产出 `.exe` artifact(ADR-0043 supersede ADR-0042 §3 的窄面)。**数据面必须保留独立 `net/http.Server`**:第三方 Agent 用 `base_url` 打 `/v1/*` 且依赖 `WriteTimeout:0` 的 SSE 流式,不可走 Wails AssetServer;Wails webview 里的 SPA 通过 AssetServer.Handler 反向代理打到本地 HTTP server 的 `/api/v1/*` + `/v1/*`。`cmd/desktop/main.go` 按 `//go:build desktop` 分两文件复用同一装配链。
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
- `GET/POST /api/v1/prompts` + `GET/PUT/DELETE /api/v1/prompts/{id}` —— prompt 收藏(§10.3-7)

### 10.3 核心页面
1. **概览**:各 Agent 调用量/token/成本/延迟汇总。
2. **Session 浏览器**:按 Agent 过滤 → 列出 session(含 SessionSource 标记)→ 进入单 session。
3. **Trace 查看器(重点)**:单 session 内请求时间线;点开看完整 messages(system/user/assistant/tool_use)、request_raw、response_raw、error_raw;支持**复制 prompt**。
4. **供应商**:表格(名称/类型/适配器/基础 URL)+ Modal 表单 CRUD,字段与 admin 完全一致(name/type 品牌预设+自定义/adapter/base_url/凭证方式 ref 或明文 key)。weight/timeouts 不在 UI 暴露——创建时写默认值(100 / 2s·5s·30s),编辑时保留原值(零值超时会让数据面失去保护);明文 key 以 `plain://` 存本地 YAML(桌面无加密凭证库)。
5. **模型**:表格展示 alias + upstreams 行内 pill(provider · 上游模型 · 输入/输出价格 · cache %);Modal 表单字段与 admin 一致(描述/context_length/capabilities/tags + 动态 upstream 行:provider 下拉、上游模型、默认 max tokens、输入/输出价格(显示美元、提交转 micro)、缓存命中 %);币种硬编码 USD。
6. **路由**:表格展示 model_alias + strategy pill + providers pill;Modal 表单与 admin 一致(model_alias 从现有模型下拉选择,strategy 含 priority/weighted/round_robin/session_affinity,候选 provider 按所选模型的 upstream 过滤,动态行权重)。
7. **收藏/打标签好 prompt(已落地)**:`prompt_templates` 表 + `/prompts` 列表页(搜索/标签筛选/复制/编辑/删除),Trace 查看器详情头部有「收藏」按钮(messages JSON 预填 + 来源会话/trace 行关联)。
8. **请求日志(已落地)**:`/request-logs`—— 多维过滤(Agent/provider/model/error_type/session_id/request_id/日期) + 分页表格,对齐 admin request-logs(无 tenant/cost 列),session 链接跳 Trace。
9. **运行日志(已落地)**:`/logs`—— 进程日志查看(3s 轮询、tail 档位、客户端关键字过滤),数据来自 ring buffer(完整历史在 logs/desktop.log)。
10. **设置(已落地)**:`/settings`—— 网关监听(addr/session_headers,重启生效)+ Trace 采集(开关/上限/留存天数,保存即生效)+ API 密钥卡片(查看/复制/轮换,新明文仅展示一次)。
11. **连通性测试(已落地)**:`/playground`—— 选模型发小请求,展示响应/耗时/命中供应商/token 用量,上游错误原样展示。

> **UI 对齐原则(2026-07)**:desktop-ui 的布局、表格、表单字段、按钮变体、Modal 结构全面镜像 admin web(`web/`),唯一事实来源是 `design/design-system.md` + `web/src`。有意的偏差仅四处:不显示 cost 列(桌面不跑计费)、provider 的 weight/timeouts 隐藏但随提交保留、明文凭证映射 `plain://`、品牌名「桌面网关助手」+ zh-CN 单语言。

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
- `Provider.adapter`:`openai` / `claude`(后端 adapter registry)
- `Provider.type`:品牌预设(openai/tencent/zhipu/anthropic/google/azure/deepseek/bedrock)+ "自定义…" 兜底 Input(与 admin 一致)
- `Provider` 凭证方式:`ref`(API 密钥引用)/ `key`(明文,存 `plain://`)
- `Route.strategy`:`priority` / `weighted` / `round_robin` / `session_affinity`
- 会话过滤器 `agent_type`:见 Sessions.tsx 常量

**Markdown 渲染**:Trace 详情中仅 `role=assistant` 的 text block 用 `react-markdown` 渲染;`thinking`/`tool_result`/`user`/`system` 保持 `<pre>` 纯文本。

---

## 11. 测试策略

正交分离 + 组合根独立测：

**已落地（每 PR 跑，无 build tag）**：
- **核心包测试不受影响**：`internal/proxy` / `internal/adapter` / `internal/observability` 等原有测试照跑（CI 不变）。
- **`internal/desktopstore/query_test.go`**：纯 unit。真 SQLite (`t.TempDir()`) 种子数据,断言 session 聚合 / request-log 列表 / trace 查询正确性。
- **`internal/desktopstore/keystore_test.go`**：种子 key 的 `LookupByHash` 命中/miss/过期、空 `AllowedModels` 全放行。
- **`internal/desktopapi/server_test.go`**：真 SQLite + `httptest` 真读 API 服务端,覆盖 7 个端点 + `%2F` request_id 边界。
- **`cmd/desktop/wiring_test.go`**（**关联影响守卫**）：in-process 装配完整桌面链路（`proxy.Router` + desktop SQLite sinks + `config.Load` 闭包 + mock 上游）,真打 `/v1/chat/completions`（流式 + 非流式）,断言 `request_logs`/`trace_payloads` 落 SQLite 且读 API 能取回。**这是唯一验证"组装后真能跑通"的测试**——若共享接口签名/语义变更未被编译器抓住,这里会抓住。模式对照 `design/e2e.md` 的 desktop 节。
- **`desktop-ui/src/lib/format.test.ts`**：前端纯函数冒烟（vitest）。组件渲染/Playwright 延后。

**手动测试脚本**：
- `scripts/desktop-test.sh`：build → 后台启动 → curl 真打 → 读 API 验证 → 清理（对照 `scripts/devstack-test.sh` 形态）。
- `scripts/desktop-web-dev.sh`：双进程开发环境（Go 网关 + Vite HMR）,依赖 `desktop-ui/vite.config.ts` 的 `server.proxy` 把 `/api/v1`/`/v1` 转到网关端口。

**配置真实上游 key**：默认 seed 的 provider 记录不含真实上游 key，首次启动后 provider 可见但调用会 401。如需真实上游调用，复制项目根目录 `.env.example` 为 `.env` 并填入 `GATEWAY_SEED_DEEPSEEK_KEY` / `GATEWAY_SEED_TOKENHUB_KEY` / `GATEWAY_SEED_KIMI_KEY`，再启动 desktop 或 adminstack。`.env` 已在 `.gitignore` 中，不会入库。

**显式延后**：Wails 壳的浏览器/原生 UI 自动化测试（成本高、收益低,Phase 3 之后）。

---

## 12. 文件级改动清单

**新增（全部在 `cmd/desktop/`，不动核心）：**
- `cmd/desktop/main.go` —— 组合根装配
- `internal/desktopstore/sqlite.go` —— SQLite 打开 + 建表 DDL
- `internal/desktopstore/keystore.go` —— `auth.KeyStore` SQLite 实现
- `internal/desktopstore/requestlog_sink.go` —— `RequestLogSink` SQLite 实现
- `internal/desktopstore/tracepayload_sink.go` —— `TracePayloadSink` SQLite 实现
- `internal/desktopstore/query.go` —— session/request/trace 读查询
- `cmd/desktop/config/load.go` —— 本地 YAML → `config.Dynamic` 闭包
- `internal/desktopapi/server.go` —— 轻量读 API
- `cmd/desktop/seed/` —— 默认 key + 默认 providers/routes 种子
- `desktop-ui/` —— React 前端（Vite SPA，顶层与 `web/` 并列；复用 `web/` 组件形态）

**复用（零改动）：** `internal/proxy` `internal/adapter` `internal/plugin` `internal/observability` `internal/auth` `internal/config/schema.go`

**可选核心增量：** 若希望 `internal/store` 增加 SQLite dialect 打开能力（非必须，桌面 store 可独立）；否则核心零改。

---

## 13. 风险与开放问题

1. **前缀哈希回退的 session 抖动**：coding agent 的 system prompt 含动态尾巴时可能拆 session（ADR-0018 §5）。缓解：桌面配置候选 session 头名覆盖各 Agent；UI 显示 `SessionSource` 让用户识别。
2. **SQLite 并发写**：录制走 `Async*Recorder` 异步刷盘（fail-open），个人量级写入不在热点；可读 API 与写并发——gorm sqlite 默认串行化，必要时开 WAL。
3. **凭证安全**：默认 key 存本地文件，单用户桌面可接受；若追求更好，接系统 Keychain（后续增强）。
4. **UI 复用度**：`web/` 组件可直接搬，但 admin 端点是 admin 包私有；桌面需自写薄读 API（§10.2），非大改。

---

## 14. MVP 分期

**Phase 0 — 骨架**：`cmd/desktop/main.go` + SQLite 打开 + 种子默认 key + 本地 YAML 配置加载；网关能起、Agent 能打透、请求被路由转发。（核心零改验证）

**Phase 1 — 录制落盘**：补 `RequestLogSink` + `TracePayloadSink` 的 SQLite 实现 + 建表；验证 request_logs / trace_payloads 落 SQLite。

**Phase 2 — 读 API + 基础 UI**：`server/server.go` 轻量读 API + Vite SPA 壳 + 概览页 / Session 浏览器 / Trace 查看器（复制 prompt）。这是"看 Agent 行为 + 学提示词"价值的首次闭环。

**Phase 3 — 打磨**：各 Agent 过滤、SessionSource 标记（UI 已做）；留存策略、可选 prompt 收藏（本轮已落地，见 §6.4 / §10.3-7）；凭证文件保护（待做）。本轮另落地：gateway 段保留修复、端口预绑定 + 冲突对话框 + Wails 单实例锁、数据目录迁 `~/.voxeltoad`、请求日志页、运行日志页、设置页、API key 管理、连通性测试页、**Windows 打包落地(ADR-0043)**。

> 范围封顶：粒度 3（ML 归纳范式）明确不做；企业特性（RBAC/operator/billing/节点注册/配置版本历史）明确不做。
