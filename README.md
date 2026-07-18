<p align="center">
  <img src="./web/public/logo.svg" alt="voxeltoad logo" width="96" height="96" />
</p>

# voxeltoad

企业级大模型网关。Go 同构（数据面 + 管理面），对外提供 OpenAI 兼容 API，统一代理
OpenAI / Claude / 腾讯混元 / 智谱 / 任意 OpenAI 兼容供应商，并提供配额计费、限流熔断、
审计、多租户等企业治理能力。

- 设计与约束规范：见 [`CODEBUDDY.md`](CODEBUDDY.md)（路由表）与 [`design/`](design/)
- 完整设计文档：[`docs/plans/2026-06-29-llm-gateway-design.md`](docs/plans/2026-06-29-llm-gateway-design.md)

> 后端核心链路已基本成型（Chat Completions 代理、Provider 适配、配额计费、限流熔断、
> 审计、多租户、Admin API）。前端控制台为初始切片，周边仍有产品与生产化缺口，详见设计文档。

## 前置要求

| 工具 | 版本 | 用途 |
|---|---|---|
| Go | ≥ 1.26 | 数据面 / 管理面 |
| Node.js + npm | ≥ 24 | TypeScript SDK |
| Docker | 可选 | 本地起 PostgreSQL（`make dev-deps`）、构建镜像 |
| golangci-lint | 可选 | `make lint`（`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`） |

> 运行态唯一有状态依赖是 **PostgreSQL**。限流/缓存默认走内存实现，Redis 仅在数据面多实例
> 需要全局精确限流时作为扩展项接入；配置下发通过数据面轮询管理面的 HTTP 快照接口完成，
> 不依赖 etcd。

## Windows 开发者

官方开发环境为 **Windows 11 + WSL2（Ubuntu 22.04+）**，所有 `make` 目标（含 PR 门禁 `make ci`）均在 WSL2 内执行。决策与取舍见 [ADR-0042](docs/adr/0042-windows-dev-environment-wsl2.md)。

> **Git-Bash 与 PowerShell 原生不是官方支持环境。** 现有 `scripts/*.sh` 多数依赖 setsid / lsof / pgrep / jq / openssl 等 POSIX 工具（见 ADR-0042 Context），在 Git-Bash 下跑不动；CI 仅 Linux（`ubuntu-latest`），不为 Windows 原生兜底。

### 前置依赖（在 WSL2 内安装）

```bash
sudo apt update
sudo apt install -y build-essential jq openssl docker.io   # make/gcc/jq/openssl/docker
# Go 1.26+ / Node.js 24+ / golangci-lint 见上表「前置要求」
```

### 三步上手

```bash
git clone <repo> && cd voxeltoad
make sdk-install   # 首次：装 TS SDK 依赖
make ci            # PR 门禁，本地全绿即可提 PR
```

### target 分级清单

| 分级 | 含义 | 示例 |
|---|---|---|
| 原生跨平台 | 仅调用 go / node / npm / git，任意平台直接跑 | `make build` / `make test` / `make vet` / `make sdk-*` / `make web-install` |
| 需 bash + coreutils | 调 bash 内建与 coreutils，Git-Bash 可跑，但官方环境仍是 WSL2 | `make fmt-check` / `make tidy` |
| 需 WSL2（POSIX-only） | 依赖 setsid / lsof / pgrep / jq / openssl 等，仅 Linux / macOS / WSL2 | `make ci` / `make devstack` / `make adminstack` / `make arch-check` / `make check-docs` / `make start-stack` |

> 本文档未在真实 Windows 机器上逐项验证；在 WSL2 内遇到问题请提 issue。

## 目录结构

```
cmd/{gateway,admin,adminstack,devstack,desktop}  数据面 / 管理面进程入口（adminstack/devstack 自包含开发栈；desktop 桌面个人网关）
internal/             核心实现（adapter / plugin / proxy / admin / config / desktopstore / desktopapi / ...）
pkg/                  对外可复用库（sse / tokenizer / errors）
sdk/typescript/       TS 薄封装 SDK + 测试
desktop-ui/           桌面个人网关前端（Vite + React，与 web/ 并列）
test/                 集成/E2E 测试、mock 上游、profile YAML
deploy/               Dockerfile、部署产物、desktop(Wails 打包层)
scripts/              构建与本地依赖脚本
design/               人与 AI 共用的约束规范
docs/plans/           设计文档
```

## 快速开始

```bash
# 1. 安装 SDK 依赖（首次）
make sdk-install

# 2. 一键本地验证（等同 CI）
make ci

# 3. 本地起 PostgreSQL（需要 Docker）—— 仅用于运行整个网关服务
make dev-deps        # 停止并清理：make dev-deps-down

# 4. 运行
make run-admin       # 管理面，监听 :8090
make run-gateway     # 数据面，监听 :8080
```

健康检查：`curl localhost:8080/healthz`、`curl localhost:8090/healthz`。

> **关于 PostgreSQL 的两种用法（不冲突）**：`make dev-deps` 用 Docker 起一个 PostgreSQL，
> 供**运行整个网关服务**时持久化配置/Key 等数据；而**跑测试**时需要库的用例改用
> `embedded-postgres`（在测试进程内拉起真实 PG，免 Docker，见
> [`design/unit-test.md`](design/unit-test.md)）。两者都是真实 PostgreSQL（不用 SQLite），
> 区别只是「进程外容器·服务运行时」vs「进程内嵌入·测试时」。

---

## 自动化工具（Make 目标）

所有自动化通过 `make` 驱动。`GO` 变量可覆盖 Go 可执行文件路径（如 `make build GO=/opt/homebrew/bin/go`）。

### 构建与运行

| 命令 | 说明 |
|---|---|
| `make build` | 构建 `bin/gateway` 和 `bin/admin` |
| `make build-gateway` / `make build-admin` | 单独构建某一进程 |
| `make run-gateway` / `make run-admin` | 直接 `go run` 启动 |
| `./scripts/build.sh` | 带版本戳（version/commit/date）构建到 `bin/`；支持交叉编译：`GOOS=linux GOARCH=amd64 ./scripts/build.sh` |
| `make clean` | 清理 `bin/` 与覆盖率产物 |

### 质量门禁

| 命令 | 说明 |
|---|---|
| `make fmt` | `gofmt -w .` 原地格式化（本地修复用） |
| `make fmt-check` | 校验 gofmt 是否干净，不改文件（`ci` 用此项） |
| `make vet` | `go vet ./...` |
| `make lint` | golangci-lint（版本固定于 `.tool-versions`，需单独安装） |
| `make audit` | 依赖漏洞扫描：govulncheck（Go）+ npm audit（SDK） |
| `make arch-check` | 架构依赖校验（4 条规则）：proxy↔admin 隔离、`pkg/` 不依赖 `internal/`、adapter 间不互引、plugin 间不互引 |
| `make tidy` | `go mod tidy` |
| `make help` | 列出所有可用 make 目标 |

### TypeScript SDK

| 命令 | 说明 |
|---|---|
| `make sdk-install` | 安装 SDK 依赖 |
| `make sdk-typecheck` | `tsc --noEmit` 类型检查 |
| `make sdk-lint` | biome 代码检查（格式 + lint） |
| `make sdk-build` | tsup 打包（ESM + CJS + d.ts）到 `sdk/typescript/dist/` |
| `make sdk-test` | vitest 单元测试 |

### 打包

| 命令 | 说明 |
|---|---|
| `make docker` | 构建 gateway + admin 镜像 |
| `make docker-gateway` / `make docker-admin` | 单独构建（多阶段 + distroless） |

### 本地依赖

| 命令 | 说明 |
|---|---|
| `make dev-deps` | 用 Docker 启动 PostgreSQL（`localhost:5432`，user/pass=postgres，db=voxeltoad） |
| `make dev-deps-down` | 停止并移除 |

---

## 测试与验证步骤

测试约束见 [`design/unit-test.md`](design/unit-test.md) 与 [`design/e2e.md`](design/e2e.md)。

### 一键全量验证

```bash
make ci
```

等价于依次执行：`fmt-check` → `vet` → `arch-check` → `test`（Go 单测，带 `-race`）→
`sdk-typecheck` → `sdk-lint` → `sdk-test`。提交前请确保通过。

### Go 单元测试

```bash
make test            # go test -race ./...（竞态检测默认开启）
make cover           # 带覆盖率，输出函数级报告
```

> 流式转发、限流计数、配置热更新等涉及并发，`-race` 始终开启。

### Go E2E / 集成测试

E2E 用 `e2e` 构建标签隔离，默认不随 `make test` 运行：

```bash
make test-e2e                                          # 默认 profile：全 mock 上游
E2E_PROFILE_PATH=./test/profiles/real-providers.yaml make test-e2e   # 真实供应商
```

**Profile + 特征标志**：测试环境配置集中在 `test/profiles/*.yaml`。`default.yaml` 用 mock
上游 + 占位 key，CI 无需任何真实供应商凭证即可跑完整套；需要真实供应商时复制
`real-providers.yaml.example` 为 `real-providers.yaml`（已 gitignore）填入真实 key。
加载器据 key 是否为占位值自动推导特征标志（`HasRealOpenAI` 等），未配置真实 key 的用例
自动 `t.Skip`。

> 需要真实 PostgreSQL 的测试：本地/单测推荐 `embedded-postgres`（进程内拉起真实 PG，免
> Docker），CI 用 testcontainers。不使用 SQLite（方言/JSONB 差异会埋坑）。

### SDK 测试

```bash
make sdk-test                          # 单元测试（默认 hermetic，不需网关）
```

SDK 的 E2E 契约测试默认跳过，需启动网关 + mock 上游后开启：

```bash
cd sdk/typescript
VOXELTOAD_E2E=1 npm run test:e2e
```

### 手动验收测试

开发或 QA 验收时推荐使用**自包含模式**：`make devstack`（数据面）和 `make adminstack`（管理面），二者均内嵌 PostgreSQL + 自动种子数据，零配置、无需 Docker。生产风格模式（`make run-gateway` + `make run-admin` + Docker PG）也一并说明。

#### 凭据速查

| 服务 | 启动命令 | 端口 | 凭据 |
|---|---|---|---|
| 管理面（自包含） | `make adminstack` | `:8090` | `root@adminstack` / `adminstack-pass-123` |
| 数据面（自包含） | `make devstack` | `:8080` | API key `sk-devstack-client`，model `chat` |
| Docker PostgreSQL | `make dev-deps` | `:5432` | `postgres://postgres:postgres@localhost:5432/voxeltoad?sslmode=disable` |

#### 模式 A（推荐）：自包含，零配置

**管理面** — 完整的运营 API（config CRUD、租户/运营者/密钥、配额、用量、审计）。

在一个终端窗口：
```bash
make adminstack
# 输出包含 super-admin 凭据：root@adminstack / adminstack-pass-123
```

在另一个终端，复制粘贴以下 curl 命令逐一验证管理面：

```bash
BASE="http://127.0.0.1:8090"

# 1. 登录（获取 token）
TOKEN=$(curl -sS -X POST "$BASE/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"root@adminstack","password":"adminstack-pass-123"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
echo "token: ${TOKEN:0:16}…"

# 2. 查看 providers 列表（空 -> 信封 {data,next_cursor}）
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/providers" | head

# 3. 创建 provider
curl -sS -X POST "$BASE/api/v1/providers" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"openai-prod","type":"openai","adapter":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"env://OPENAI_KEY"}'

# 4. 创建 model（上游 provider 必须已存在）
curl -sS -X POST "$BASE/api/v1/models" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"alias":"gpt-4o-mini","upstreams":[{"provider":"openai-prod","upstream_model":"gpt-4o-mini"}]}'

# 5. 创建 route
curl -sS -X POST "$BASE/api/v1/routes" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"model_alias":"gpt-4o-mini","strategy":"priority","providers":[{"name":"openai-prod"}]}'

# 6. 创建租户 + 租户管理员
curl -sS -X POST "$BASE/api/v1/tenants" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"acme"}'
curl -sS -X POST "$BASE/api/v1/operators" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"ta@acme","password":"ta-pass-123","role":"tenant-admin","tenant_id":2}'

# 7. 配额充值
curl -sS -X POST "$BASE/api/v1/quotas/topup" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"scope":"tenant:acme","delta":10000000,"currency":"usd"}'

# 8. 读取用量 / 审计
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/usage?limit=5"
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/audit?limit=5"
```

**浏览器 UI 验收** — 用浏览器直连 Control Panel 前端（切片 0：登录 → providers 增删），替代 curl 手工拼请求。

保持 `adminstack` 运行在 `:8090`，另开一个终端：
```bash
make sdk-build      # 首次：生成 SDK dist（file: 依赖）
make web-install    # 首次：安装 web 依赖 + Playwright chromium
make web-dev        # 启动 Next.js 开发服务器（:3000 + 热重载）
```

浏览器打开 `http://localhost:3000`，用 `root@adminstack` / `adminstack-pass-123` 登录。登录后可见 providers 列表、创建表单和删除按钮 — 所有操作经过 Next.js 服务端（加密 cookie 持有 operator token）→ adminstack（Bearer）→ PostgreSQL。更多 UI 细节见 [`design/frontend.md`](design/frontend.md) §11。

```bash
# 停止 web：Ctrl-C
# adminstack 仍在运行，Ctrl-C 停止
```

**一键启动管理面 + 前端** — `make start-stack` 在一个命令里启动 adminstack（内嵌 PG + 管理面 :8090）和 web-dev（:3000），Ctrl-C 一起停。详见 `scripts/start-stack.sh`。

**演示数据与持久化** — 默认情况下每次 `start-stack` 从空库启动：除了 super-admin（`root@adminstack` / `adminstack-pass-123`），还会自动种入一套演示数据，方便开箱即用：

| 资源 | 内容 |
|---|---|
| providers | `深度求索`（type=deepseek, adapter=openai）、`TokenHub`（type=tencent, adapter=openai，各带上游密钥，UI 里换成真实 key 即可） |
| models | `deepseek-v4-flash`、`deepseek-v4-pro`（跨深度求索/TokenHub 双上游故障转移）、`hy3`、`kimi-k2.7-code` |
| tenant | `demo-tenant` / group `default`，配额已注资 |
| api key | `sk-demo-tenant-key-0001`（key_id `demo-key-001`），allowedModels 含以上 4 个 model — 数据面可直接用 |
| operator | `tenant-admin@demo` / `demo-pass-123`（租户管理员，登录可看租户视图） |

两个环境变量控制行为：

```bash
GATEWAY_PERSIST_DATA=1 make start-stack   # 数据持久化：Ctrl-C 后数据保留，重启复用（默认关闭=每次清空）
GATEWAY_SEED_DEMO=0    make start-stack   # 不种演示数据（默认开启）
```

- 持久化模式下数据目录在 `$TMPDIR/voxeltoad-adminstack-pg/data`，删掉即重置。
- 种子是幂等的（store 层全部走 `ON CONFLICT` upsert），持久化模式下重复启动不会产生重复行、不会覆盖你的改动。

**在 `start-stack` 运行时启动真实数据面** — adminstack 不起数据面，但你可以另开一个终端，在不打断 start-stack 的情况下把真正的 `cmd/gateway` 拉起来做端到端测试（:8080，从管理面热加载配置、解密你在 UI 里填的 provider 明文 API key）：

```bash
make start-gateway   # 需先在另一终端跑 `make start-stack`
# 数据面 → http://127.0.0.1:8080   管理面 → http://127.0.0.1:8090   web UI → http://localhost:3000
# Ctrl-C 只停 gateway，admin + web 继续运行
```

脚本会从 adminstack 日志里恢复随机端口的内嵌 PG DSN，并复现 adminstack 的 dev KEK（仅本地用，见 `scripts/start-gateway.sh`）。如果你只想要一个零依赖、独立的数据面做 curl/SDK 烟测，用下面的 `make devstack`。

**数据面** — OpenAI 兼容的聊天补全 API（内嵌 mock 上游）。

在一个终端窗口：
```bash
GATEWAY_ALLOW_INSECURE_DEV=1 make devstack
# 输出包含 API key 和 model 别名
```

在另一个终端：
```bash
GW="http://127.0.0.1:8080"

# 健康检查
curl -sS "$GW/healthz"

# 非流式聊天（seed 数据：model=chat，key=sk-devstack-client）
curl -sS -X POST "$GW/v1/chat/completions" \
  -H "Authorization: Bearer sk-devstack-client" \
  -H 'Content-Type: application/json' \
  -d '{"model":"chat","messages":[{"role":"user","content":"hi"}]}'

# 流式聊天（SSE）
curl -sS -N "$GW/v1/chat/completions" \
  -H "Authorization: Bearer sk-devstack-client" \
  -H 'Content-Type: application/json' \
  -d '{"model":"chat","stream":true,"messages":[{"role":"user","content":"hi"}]}'

# 无效 key → 401
curl -sS -w '\n%{http_code}\n' "$GW/v1/chat/completions" \
  -H "Authorization: Bearer bad-key" \
  -H 'Content-Type: application/json' \
  -d '{"model":"chat","messages":[{"role":"user","content":"hi"}]}'
```

#### 模式 B：生产风格启动（需要 Docker PostgreSQL）

管理面启动前需先引导超管；数据面启动时从管理面拉取 config 快照，因此管理面需先启动。

```bash
# 1. 起 PostgreSQL（Docker）
make dev-deps

# 2. 引导第一个 super-admin（idempotent：已有则不重复创建）
make run-admin -- -bootstrap -email admin@test.com -password test123456

# 3. 启动管理面（后台）和数据面（前台，方便 Ctrl-C 停掉）
make run-admin &
sleep 2
GATEWAY_ALLOW_INSECURE_DEV=1 make run-gateway
```

之后用与模式 A 相同的 curl 命令，替换凭据：管理员凭据调整为 `admin@test.com / test123456`，数据面需先通过管理面创建 provider→model→route→租户→key→充值后才有可用端点。

#### 关键环境变量

| 变量 | 用途 |
|---|---|
| `GATEWAY_ALLOW_INSECURE_DEV=1` | 绕过数据面↔管理面之间的内部 token 校验（仅开发/测试环境） |
| `GATEWAY_CONFIG` / `ADMIN_CONFIG` | 指定 gateway/admin 的 bootstrap YAML 路径（默认用 `config.example.yaml` 的硬编码值） |

Ctrl-C 停止所有服务。模式 A 无需手动清理（嵌式 PG 自动销毁）；模式 B 的 Docker PG 用 `make dev-deps-down` 停止并清理数据。

> 管理面的完整 API 契约见 [`docs/openapi/admin.yaml`](docs/openapi/admin.yaml)，更详细的业务流程与状态设计见 [`design/domain-flows.md`](design/domain-flows.md)。
> 前端 UI 的手动启动方式见 [`design/frontend.md`](design/frontend.md) §11。

---

## 桌面个人网关（cmd/desktop）

个人开发者的本地 LLM 入口：把多个供应商（深度求索 / TokenHub / Kimi 等）收敛到一个本地代理（`127.0.0.1:8787`），被动录制所有调用的 prompt + completion，用于分析与学习提示词写法。**复用企业版数据面**（proxy / adapter / plugin / observability / auth），**不构建** admin / RBAC / billing。详见 [`design/desktop.md`](design/desktop.md) 与 ADR-0041。

### 快速启动（开发模式）

```bash
make desktop-web-dev    # Go 网关 :8787 + Vite SPA :5173
```

浏览器开 `http://127.0.0.1:5173`，sidebar 顺序：概览 / 供应商 / 模型 / 路由 / 会话浏览器。默认配置含 3 家真实供应商（深度求索 / TokenHub / Kimi-code，key 在 YAML 里需自行替换为有效值）。

第三方 Agent（CodeBuddy / Codex / Claude Code 等）配置：

```
base_url = http://127.0.0.1:8787/v1
api_key  = desktop-local-default-key
model    = deepseek-v4-flash  # 或 deepseek-v4-pro / hy3 / kimi-k2.7-code / kimi-for-coding
```

### 构建 macOS .app

```bash
# 一次性安装 Wails CLI（go install 的位置可能不在 PATH 上，脚本会自动找 $(go env GOPATH)/bin/wails）
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make desktop-build
open deploy/desktop/build/bin/desktop-gateway.app
```

构建脚本（`scripts/build-desktop.sh`）会：① `npm ci && npm run build`（desktop-ui）② 拷 `dist/` 到 `deploy/desktop/dist/` ③ `wails build -tags desktop -platform darwin/universal`。产出 `.app`（约 35MB）。双击启动：Wails 窗口加载 SPA，菜单栏支持 重载配置（Cmd+R）/ 打开配置文件位置 / 复制 API key（Cmd+Shift+K）。关闭窗口会隐藏到 dock（HTTP server 继续跑，Agent 还能打）。

### 测试

```bash
make desktop-test   # go test ./cmd/desktop/...（wiring + store + server）
make desktop-e2e    # 手动 smoke：build mock-upstream + desktop → curl /v1 + /api/v1 → cleanup（9 断言）
```

桌面版是共享契约（KeyStore / Sinks / config.Dynamic / proxy.Router）的**编译期 + 运行期双层 canary**：`internal/desktopstore` + `internal/desktopapi` + `cmd/desktop` 都无 build tag，`make test` 每 PR 编译它们。

---

## 约束与设计规范

本仓库的规则以 [`CODEBUDDY.md`](CODEBUDDY.md) 为路由表，具体规范在 `design/`：

- [`design/architecture.md`](design/architecture.md) — 分层模型、依赖规则、「新增供应商/插件改哪些文件」对照表
- [`design/unit-test.md`](design/unit-test.md) — 测什么 / 不测什么、Go 测试模式
- [`design/e2e.md`](design/e2e.md) — mock 上游、SSE 流式断言、profile、Pitfalls
- [`design/observability.md`](design/observability.md) — 每条 LLM 请求必须记录的语义字段与门禁
