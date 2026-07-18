# ADR-0041: Desktop personal gateway — same-repo, orthogonality, K1 seed-key

- Status: Accepted
- Date: 2026-07-16
- Builds on ADR-0006 (API key auth), ADR-0017 (RBAC), ADR-0021 (request_logs), ADR-0033 (data-plane keys not bound to roles), ADR-0039 (trace_payloads)
- SoT: `design/desktop.md`（需求与实现细节）、`design/architecture.md` §三入口依赖矩阵（依赖矩阵与共享契约面）

## Context

个人开发者日常有多条 LLM 调用源（CodeBuddy / Codex / Claude Code 等第三方 Agent + 自己的脚本），希望把多个供应商收敛到一个本地入口、被动录制所有调用以便学提示词。这是一款**桌面个人网关**。

企业级网关（数据面 `internal/proxy` + 管理面 `internal/admin`）已存在。问题：桌面版该**fork**、**抽独立 module**、还是**同仓新增组合根**？鉴权该走完整 RBAC 还是简化？配置来源如何处理？

## Decision

### 1. 同仓新增 `cmd/desktop/` 组合根，不 fork、不抽 module

核心层（`internal/proxy` / `adapter` / `plugin` / `observability` / `auth` / `config/schema`）**只读、随仓前进**。桌面版与企业版的差异**正交**,收敛在两处新增 `internal/` 包 + 一个组合根:
- `internal/desktopstore/`(L2,与 `internal/store` 平级)—— SQLite 实现的 `KeyStore` / `RequestLogSink` / `TracePayloadSink` + 读查询。
- `internal/desktopapi/`(L3,与 `internal/admin` 平级)—— 桌面读 API(stdlib net/http)。
- `cmd/desktop/`(L4 组合根)—— 装配 `proxy.Router` + `desktopstore` + `desktopapi` + 本地 YAML 配置闭包 + 首次启动种子。

**分层对齐而非塞进 cmd/**:desktop 的 L2/L3 代码与企业版同层包平级,而不是塞进 `cmd/desktop/`(后者是纯装配,与 `cmd/gateway`/`cmd/admin` 同构)。前端工程 `desktop-ui/` 顶层与 `web/` 并列。

### 2. 正交性论据：不构建 admin = 自动无 RBAC

`internal/proxy ⊥ internal/admin`（依赖规则 1，已由 import 检测验证）；`internal/authz` 仅被 `internal/admin` 使用。因此 desktop 不 import admin 即**自动无 RBAC 检查**。

数据面唯一的权限闸门是 `modelAllowed`（`auth_middleware.go:29`），空 `AllowedModels` = 全部放行——"默认租户全权限"在数据面**结构性成立**，无需任何 RBAC 代码。这是 ADR-0033（"数据面 keys 不绑 role"）的自然推论在桌面场景下的极致形态。

### 3. K1 种子默认 key（而非 K2 passthrough）

鉴权选 K1：种子 1 个 API key（`KeyRecord{AllowedModels:[]}`），第三方 Agent 配 `base_url=http://127.0.0.1:<port>/v1`、`Authorization: Bearer <default-key>`。`authMiddleware` 真跑真通过，proxy **零改动**。

未选 K2（加 `auth.disabled` passthrough 让 Agent 免填 key）：会污染数据面核心，破坏"desktop 核心零改"的正交性。

### 4. 配置来源：本地 YAML 闭包，替换 admin 快照轮询

`app.NewDispatcherWatcher`、`proxy.WithSettingsSource`、`billing.NewPlugin` 接收 `func() *config.Dynamic`。desktop 直接 `yaml.Unmarshal` 本地 YAML 到 `config.Dynamic` 并传闭包——**核心包不感知 config 来源变了**。启动设 `GATEWAY_ALLOW_INSECURE_DEV=1` 跳过 `Bootstrap.Validate` 对空快照的 `internal_token_ref` 校验（开发态已有开关）。

## Consequences

- **共享契约面**（变更这些接口 = desktop 同步受影响）：`auth.KeyStore`、`observability.RequestLogSink` / `TracePayloadSink`、`config.Dynamic` + `GatewaySettings`、`proxy.Router` + `With*` 选项、`config/schema.go` 结构体。完整矩阵见 `design/architecture.md` §三入口依赖矩阵。
- **编译期 canary**:`internal/desktopstore/` + `internal/desktopapi/` + `cmd/desktop/` 都**无 build tag**(对照 `cmd/devstack` / `cmd/adminstack` 用 `-tags`),`make test` 每 PR 跑 `go test -race ./...` 编译它们。任何共享接口签名变更首先撞 desktop 实现的编译失败。desktop 是共享契约的**编译期守卫**。
- **完整"组装后真能跑通"由 `cmd/desktop/wiring_test.go` 守卫**：in-process 装配 `proxy.Router` + SQLite sinks + mock 上游,真打 `/v1/chat/completions`。这是编译期检测之外的运行期 canary（见 `design/desktop.md` §11 / `design/e2e.md` desktop 节）。
- **admin/gateway 改共享文件时的关联影响**：改 `internal/auth`/`observability`/`config`/`proxy` 时,desktop 作 canary 先撞；改 `internal/admin`/`authz` 对 desktop 无影响（正交）。
- **未来 Wails 打包（未落地）**：数据面必须保留为独立 `net/http.Server`（SSE 流式 + 第三方 Agent `base_url` 不走 WebView），Wails 仅承担原生壳（托盘、菜单）。打包层位于 `deploy/desktop/`。
