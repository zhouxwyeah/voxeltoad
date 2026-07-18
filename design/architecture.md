# Architecture

> 数据面/管理面同构 Go 单体仓库。本文件定义分层模型、依赖规则与「需求 → 改哪些文件」对照表。
> **设计目标**：每个需求都能映射到一组清晰、最小的待改文件。横切能力放底层，供应商/治理逻辑各归其位。

## Layer Model

```
L0  pkg/              对外可复用的纯库（无业务依赖：errors、sse、tokenizer 封装…）
L1  internal/config/  配置加载 + 数据面配置快照轮询器（poller）
    internal/observability/  OTel / metrics / logging —— 横切基础设施
L2  internal/adapter/   供应商协议适配（openai / claude；tencent/zhipu/deepseek 等复用 openai adapter，ADR-0001）
    internal/plugin/    插件框架 + 内置治理插件（限流 / 缓存 / 敏感词 / PII 脱敏 / 注入检测 / 外部审查）
    internal/normalize/ 请求归一化（max_tokens 默认、system 消息合并，ADR-0009）
    internal/billing/   token 计费与配额（pre-debit/settle，直连 PG quota store，ADR-0013）
    internal/credential/ 供应商凭证加密（AES-256-GCM，db://provider/<name> scheme，ADR-0031）
    internal/auth/      API Key 鉴权（数据面，ADR-0006）
    internal/authz/     权限 catalog + requirePermission 中间件（管理面 RBAC，ADR-0017）
    internal/operator/  operator 创建/认证/session 管理（管理面，ADR-0017）
    internal/apperr/    错误码 catalog（分域文件 + HTTP status + i18n key，见 design/frontend.md §12）
    internal/store/     PostgreSQL 持久化（gorm）
    internal/desktopstore/  桌面 SQLite 持久化（KeyStore/Sinks/Query，与 internal/store 平级，ADR-0041）
L3  internal/proxy/     数据面编排：路由、转发、流式处理、插件链执行
    internal/admin/     管理面 API + 服务层 + 配置下发
    internal/desktopapi/    桌面读 API（与 internal/admin 平级，ADR-0041）
    internal/app/       数据面组合根（stores/watcher 装配，见 docs/plans/2026-07-01-data-plane-prd.md）
L4  cmd/gateway/        数据面进程入口
    cmd/admin/          管理面进程入口
    cmd/adminstack/     自包含管理面（内嵌 PG + 种子；开发验收用，build tag）
    cmd/devstack/       自包含数据面（内嵌 PG + mock 上游；开发验收用，build tag）
    cmd/testpg/         共享 embedded PG（stack-test-all 用，三个 stack 测试共用一份 PG；build tag）
    cmd/desktop/        桌面个人网关入口（SQLite + 本地 YAML + 内嵌 SPA；复用数据面，无 admin/RBAC；ADR-0041）
```

## Dependency Rules

高层可依赖低层，**反向禁止**。

```
cmd/ ──→ proxy|admin ──→ adapter|plugin|billing|auth|store ──→ config|observability ──→ pkg/
```

硬性规则：

1. **数据面与管理面隔离**：`internal/proxy/` 不得 import `internal/admin/`，反之亦然。两者只通过配置快照（数据面轮询管理面的 HTTP 配置快照接口）间接通信，不共享进程内状态。
2. **adapter 之间不互相 import**：每个供应商适配器自包含。共享逻辑下沉到 `pkg/` 或 `internal/adapter/shared`。
3. **plugin 之间不互相 import**：插件相互独立，共享逻辑下沉。
4. **L0 `pkg/` 不得 import 任何 `internal/`**：保持纯净可复用。
5. **observability/config 是横切层**：可被任意上层 import，但自身不 import 业务包（adapter/plugin/proxy/admin）。
6. **禁止 barrel 转导出**：不写只做 re-export 的 `xxx.go`，直接从源文件 import，便于依赖检测精确。

依赖关系由 import-linter 守护（`go-arch-lint` 或自写脚本），CI 强制检查 —— 运行 `make arch-check`。

## 三入口依赖矩阵（desktop / gateway / admin）

仓库有三个生产入口（`cmd/gateway` / `cmd/admin` / `cmd/desktop`）和两个开发验收入口（`cmd/adminstack` / `cmd/devstack`，build tag 守护）。下表是 `go list -deps` 核实的 `internal/` 依赖集：

| 入口 | internal/ 包数 | 独有包 | 关键差异 |
|---|---|---|---|
| `cmd/desktop` | 17 | —（gateway 的子集 + desktopstore/desktopapi） | 不需要 `plugin/ratelimit`（个人单用户无多租户公平诉求）；不构建 admin/RBAC/billing；持久化/读 API 用自己的 SQLite 实现（`internal/desktopstore` + `internal/desktopapi`）替代 `internal/store` + `internal/admin` |
| `cmd/gateway` | 16 | `plugin/ratelimit` | 完整数据面 |
| `cmd/admin` | 14 | `admin/`、`authz/` | 唯一引入管理面 + RBAC；不直接 import `proxy` |

### 共享契约面（变更这些接口 = 三入口同时受影响）

- `internal/auth.KeyStore`（`auth.go:42`，单方法 `LookupByHash`）—— desktop/gateway 都实现：desktop 实现在 `internal/desktopstore/keystore.go`(SQLite)、gateway 实现在 `internal/store/key.go`(PG)。`auth.Authenticator` 在数据面真跑。
- `internal/observability.RequestLogSink`（`requestlog.go:66`）/ `TracePayloadSink`（`tracepayload.go:65`）—— 同上,desktop 实现在 `internal/desktopstore/{requestlog,tracepayload}_sink.go`,`Async*Recorder` 异步刷盘。
- `internal/config.Dynamic` + `GatewaySettings` —— desktop 用**本地 YAML 闭包**喂给这些闭包消费者（`app.NewDispatcherWatcher` / `proxy.WithSettingsSource` / `billing.NewPlugin`），gateway 用 **admin 快照轮询**喂同一批闭包消费者。核心包不感知差异。
- `internal/proxy.Router` + 全套 `With*` 选项 —— desktop/gateway **原样复用**，零差异。
- `internal/config/schema.go`（`Provider` / `Model` / `Route` / `GatewaySettings`）—— YAML/快照共用结构体。

### 正交性结论（ADR-0041）

`internal/proxy ⊥ internal/admin`（依赖规则 1）。因此 desktop 不构建 admin 即**自动无 RBAC**：数据面唯一的权限闸门是 `modelAllowed`（`auth_middleware.go:29`），空 `AllowedModels` = 全部放行——"默认租户全权限"在数据面**结构性成立**，无需任何 RBAC 代码。desktop 通过种子 1 个空 `AllowedModels` 的默认 key（K1 决策，见 `design/desktop.md` §8 / ADR-0041）即可工作。

### 编译期安全（关联影响守卫）

`cmd/desktop/` + `internal/desktopstore/` + `internal/desktopapi/` **无 build tag**(对照 `cmd/devstack`/`cmd/adminstack` 用 `-tags`)。`make test` 每 PR 跑 `go test -race ./...`,编译三者的全部实现。任何共享接口(`KeyStore` / `RequestLogSink` / `TracePayloadSink` / `config.Dynamic`)签名变更首先撞 desktop 实现的编译,失败立即可见——**desktop 是共享契约的编译期 canary**。完整"组装后真能跑通"由 `cmd/desktop/wiring_test.go` + `internal/desktopapi/config_handlers_test.go` 守卫(见 `design/e2e.md` desktop 模式)。

## Directory Layout

```
voxeltoad/
├── cmd/
│   ├── gateway/         # 数据面进程入口（main.go）
│   ├── admin/           # 管理面进程入口（main.go）
│   ├── adminstack/      # 自包含管理面（内嵌 PG + 种子；开发验收用，build tag）
│   ├── devstack/        # 自包含数据面（内嵌 PG + mock 上游；开发验收用，build tag）
│   ├── testpg/          # 共享 embedded PG（stack-test-all 用，三个 stack 测试共用一份 PG；build tag）
│   └── desktop/         # 桌面个人网关入口（SQLite + 本地 YAML + 内嵌 SPA；复用数据面，无 admin）
├── internal/
│   ├── proxy/           # 数据面核心：router.go / forward.go / stream.go / chain.go
│   ├── adapter/
│   │   ├── adapter.go   # Adapter 接口定义 + 注册表（registry）
│   │   ├── openai/      # OpenAICompatibleAdapter（openai/tencent/zhipu/deepseek 等复用）
│   │   └── claude/      # ClaudeAdapter（协议差异大，单独实现）
│   ├── plugin/
│   │   ├── plugin.go    # Plugin 接口 + Phase（Pre/Post）+ 链执行
│   │   ├── ratelimit/   # 限流（接口 + 内存令牌桶默认；Redis 实现为多实例扩展项）
│   │   ├── cache/       # 响应缓存（接口 + 内存 TTL 默认；Redis 实现为扩展项）
│   │   ├── sensitive/   # 敏感词
│   │   ├── pii/         # PII 检测与脱敏
│   │   ├── moderation/  # 外部内容审查 API 集成
│   │   └── injection/   # Prompt 注入与 jailbreak 检测
│   ├── billing/         # 计费：usage 入账、定价计算
│   ├── normalize/       # 请求归一化（max_tokens 默认、system 提取合并，ADR-0009）
│   ├── credential/      # 供应商凭证加密（AES-256-GCM，ADR-0031）
│   ├── operator/        # 运营者/会话管理（管理面，ADR-0017）
│   ├── auth/            # apikey.go / jwt.go
│   ├── authz/           # 权限 catalog + requirePermission 中间件（管理面，ADR-0017）
│   ├── apperr/          # 错误码 catalog（分域：auth/tenant/provider/...，每域一个文件）
│   ├── config/          # config.go（bootstrap loader）/ poller.go（快照轮询）
│   ├── store/           # models.go（gorm）/ 各 repository（含 config_snapshots / data_plane_nodes）
│   ├── desktopstore/    # 桌面 SQLite 持久化（KeyStore/Sinks/Query；与 store/ 平级，ADR-0041）
│   ├── desktopapi/      # 桌面读 API（与 admin/ 平级；stdlib net/http，ADR-0041）
│   └── observability/   # otel.go / llm_attributes.go
├── pkg/                 # sse/ tokenizer/ errors/ —— 对外可复用
├── api/                 # （历史遗留，仅剩 README）真实 OpenAPI 定义在 docs/openapi/admin.yaml
├── sdk/typescript/      # TS 薄封装 SDK + 测试集合
├── web/                 # React 管理面前端（Control Panel）
├── desktop-ui/          # 桌面个人网关前端（Vite + React，与 web/ 并列；ADR-0041）
├── deploy/{Dockerfile.*,helm,grafana,desktop}/  # 部署产物 + Grafana + 桌面 Wails 打包层(desktop/ 见 ADR-0041)
├── test/                # 集成测试 / mock 上游
├── design/             # 本目录：项目约束规范
├── bin/                 # 构建产物输出目录（gitignore；admin/gateway/adminstack/desktop 等二进制）
├── scripts/             # 校验脚本（check-docs.sh / check-errors.sh / check-permissions.sh / arch-check.sh 等）
├── docs/plans/         # 设计文档
└── docs/adr/            # 架构决策记录（ADR 0001-0041）
```

## Common Tasks: Where to Put Code

| 任务 | 层 | 改/加哪些文件 |
|---|---|---|
| **新增一个供应商适配器** | L2 | ① `internal/adapter/<name>/adapter.go` 实现 `Adapter` 接口；② 在 `internal/adapter/adapter.go` 注册表登记；③ `internal/config/schema.go` 加供应商配置项；④ `test/mock-upstream/<name>.go` 加 mock。若兼容 OpenAI 协议，优先复用 `adapter/openai`，仅在配置层加 base_url/鉴权头/模型名映射 |
| **新增一个治理插件** | L2 | ① `internal/plugin/<name>/` 实现 `Plugin` 接口（声明 Phase）；② 在 `internal/plugin/plugin.go` 注册；③ 管理面加该插件的配置 CRUD |
| **新增一种鉴权方式** | L2 | `internal/auth/` 加实现 |
| **修改请求处理流程/插件链顺序** | L3 | `internal/proxy/chain.go` |
| **修改路由/负载均衡/故障切换策略** | L3 | `internal/proxy/router.go` |
| **新增管理面资源 CRUD** | L3 | `internal/admin/` 加 handler + service；`internal/store/` 加 model + repository |
| **新增/修改错误码** | L2 | `internal/apperr/<domain>.go` 加 `apperr.New(code, status, i18n)`；同步在 `web/src/locales/{en,zh}/errors/<domain>.json` 加 key；`make check-errors` 校验 |
| **新增配置项（需热更新）** | L1 | `internal/config/config.go`（bootstrap）或 dynamic 快照结构 + 确保 `poller.go` 原子替换覆盖 |
| **新增可复用纯工具（无业务依赖）** | L0 | `pkg/` |
| **新增 LLM 语义可观测字段** | L1 | `internal/observability/llm_attributes.go`（见 design/observability.md） |
| **改对外 API 契约** | — | 先改 `docs/openapi/admin.yaml`（单一事实来源），再同步 SDK 与前端 |

## Conventions

- **接口先行**：adapter / plugin / store repository 都先定义接口，再写实现，便于 mock 与测试。
- **配置即数据**：供应商、模型、路由、插件参数等**配置类**数据由管理面写 PG，数据面**轮询管理面的 HTTP 配置快照接口**（`/internal/config/snapshot`，带 version/ETag 条件请求）拉取，加载进内存 `atomic.Pointer` 原子替换，**不重启、不 reload signal**。配置变更低频，秒级传播延迟可接受；以此避免引入 etcd。
- **配额是例外（ADR-0013）**：配额是钱，需跨实例强一致，**不能**走最终一致的快照。数据面因此持有一条直连配额存储（PG）的连接，在请求热路径上做原子预扣/结算（`TryDebit`/`Settle`），不可达时 fail-closed。故数据面并非纯无状态——它**轮询快照 + 直连配额存储**；PostgreSQL 是**数据面与管理面共同**的唯一有状态依赖。密钥走缓存/快照（最终一致，ADR-0006），用量记录异步落库（fail-open，ADR-0016），均不需热路径强一致连接。
- **数据面节点注册（ADR-0024）**：每 proxy 实例启动时自注册到 `data_plane_nodes` 表（fail-open），周期性心跳（15s），SIGTERM 时标记下线。管理面后台 goroutine（60s）清理僵尸节点（>45s 无心跳）。节点清单仅供可观测性，不用于路由发现（路由走 Ingress/LB）。
- **配置版本历史（ADR-0025）**：每次配置变更异步保存完整快照到 `config_snapshots` 表（fail-open，不阻塞写入）。提供 history/list/get/diff/rollback/preview API，支持版本浏览、差异对比及一键回滚。
- **错误统一**：管理面与数据面 handler 通过 `internal/apperr/` 的分域错误码 catalog
  返回 `{"error":{"message":<i18n key>,"type":<code>}}`（OpenAI 兼容形状）。新增错误
  码在对应域文件（`auth.go`/`tenant.go`/...）里 `apperr.New(code, status, i18nKey)`，
  `make check-errors` 校验唯一性 + HTTP status 合法 + i18n key 在
  `web/src/locales/en/errors/<domain>.json` 中存在。`pkg/errors` 留作纯工具。
- **Import 路径**：同包内相对引用；跨包用完整模块路径，不使用 barrel re-export。
- **资源命名**：配置类资源以 name 为主键，结构遵循 `Metadata + Spec + Status` 模式。
