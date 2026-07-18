# 企业级大模型网关（voxeltoad）设计文档

> 日期：2026-06-29
> 状态：设计已确认，待进入实现
> 定位：企业内部统一大模型网关，对标 LiteLLM / Higress（AI 网关能力），但聚焦企业级治理与可控性
>
> **⚠ 历史快照须知**：本文是 **2026-06-29** 的初始设计快照，保留原始上下文与决策理由。部分选型已被后续 **ADR** 推翻或细化，以 `design/` 规范与 `docs/adr/` 为权威现状。以下为已确认的差异：
>
> | 本文描述 | 当前现状 | 取代依据 |
> |---|---|---|
> | L65 前端选型 **React（前后端分离）** | **Next.js App Router**（BFF，server 侧持有 operator token） | [ADR-0020](../adr/0020-control-panel-bff-auth-topology.md) |
> | L251 `provider_credentials` **已 defer**（加密落库待新 ADR） | **已实现**（AES-256-GCM，迁移 00009，`db://provider/<name>` scheme） | [ADR-0031](../adr/0031-provider-credential-encryption-at-rest.md) |
> | L87 数据面"**无状态横向扩展**" | 数据面**非纯无状态**——持有配额 PG 直连做 pre-debit/settle（fail-closed） | [ADR-0013](../adr/0013-quota-data-plane-access.md) |
>
> 正文**保留原文未改动**（遵循 ADR 不可变原则：supersede rather than edit）。

---

## 1. 目标与范围

### 1.1 核心能力分级

| 优先级 | 能力 | 说明 |
|---|---|---|
| **P0** | API 代理（A） | 多供应商统一为 OpenAI 兼容 API，业务方一套接口接入 |
| **P0** | 企业级治理（B） | 配额/计费、API Key 管理、审计、敏感词、多租户 |
| **P0** | 流量调度与可靠性（C） | 限流、熔断、降级、多供应商负载均衡、故障切换、缓存 |
| **P1** | AI 应用基础设施（D） | prompt 管理、RAG 路由、Agent 工具调用等（仅预留扩展点） |

### 1.2 非目标（当前版本明确不做）

- 不做向量库/RAG 的具体实现（仅预留插件入口）
- 不做模型训练/微调相关能力
- 首版不强绑 K8s（CRD/Operator 推迟）

---

## 2. 技术选型总览

### 2.1 整体路线：Go 同构（数据面 + 管理面均为 Go）

理由：
- LLM 网关核心难点是「流式协议适配 + 多供应商抽象 + token 级计费」，自研可控性远高于基于 Envoy 改造
- Go 的协程模型 + `net/http` 对 SSE 长连接流式转发天然契合
- 单二进制部署、容器镜像小、运维简单，契合「先 VM 后 K8s」的演进路径
- LLM 基础设施生态（OneAPI、new-api、Higress、云原生组件）事实标准是 Go，参考实现多
- 团队 Java/Python 背景转 Go 成本低（2-4 周）

### 2.2 选型清单

| 组件 | 选型 | 备选 | 理由 |
|---|---|---|---|
| 数据面 HTTP 框架 | `net/http` + `chi` 路由 | `gin` / `fiber` | 流量以流式转发为主，框架越薄越好；chi 中间件机制契合插件链 |
| 管理面框架 | `gin` + `gorm` | `kratos` / `go-zero` | 管理面轻量，用最熟方案，不上重型微服务框架 |

> **Superseded by 实现**：实际使用 stdlib `net/http`（见 `internal/admin/server.go` 与 `internal/desktopapi/server.go`），未引入 gin/gorm。
| 配置下发 | 数据面轮询管理面 HTTP 配置快照（version/ETag） | etcd / Nacos | 配置变更低频，秒级传播足够；避免引入配置中心，PG 为唯一有状态依赖 |
| 持久化 | PostgreSQL | MySQL | JSONB 适合存模型/插件参数配置 |
| 本地/测试 PG | `embedded-postgres`（Go 进程内真实 PG）+ testcontainers（CI） | — | 免 Docker、与生产同方言；不用 SQLite 避免方言差异 |
| 限流/缓存 | 接口 + 内存实现（默认）；Redis 实现为多实例扩展项 | — | 单实例内存令牌桶即精确；横向扩展时才需 Redis 共享计数 |
| 流式协议 | SSE 原生 | — | OpenAI 兼容协议即 SSE，不引 WebSocket |
| 可观测性 | OpenTelemetry + Prometheus + Loki | — | Trace/Metric/Log，LLM 自定义 attribute |
| 插件机制 | 内置 Go interface + `expr-lang/expr` | WASM(`wazero`) 留 P1 扩展点 | 协议适配走 Go 接口，治理规则走 expr |
| 鉴权 | API Key（数据面）+ JWT（管理面） | OAuth2 | API Key 是 LLM 网关标配 |
| Token 计费 | 优先 usage 字段 + `pkoukk/tiktoken-go` 兜底 | — | 本地 tokenizer 仅用于预估/限流前置 |
| 配置热更新 | HTTP 快照轮询 + 内存原子替换（atomic.Pointer） | — | 不重启、不 reload signal |
| 前端管理 UI | React（前后端分离） | — | P1 实现，先 API 优先 |

> **Superseded by ADR-0020**：实际采用 Next.js App Router BFF（见头部对照表）。
| 客户端 SDK | TypeScript，薄封装 | — | 在 OpenAI SDK 之上注入企业能力 |

---

## 3. 架构设计

### 3.1 部署形态：管理面/数据面分离 + 配置快照轮询

```
┌─────────────────────────────────────────────────┐
│  管理面 (admin-server)  - Go, 轻量              │
│  - REST API + React Web UI (P1)                 │
│  - 配额/Key/路由/插件/供应商 CRUD               │
│  - PostgreSQL (持久化，唯一有状态依赖)          │
│  - 暴露配置快照接口 /internal/config/snapshot   │
└──────────────────┬──────────────────────────────┘
                   ▲ 轮询快照 (version/ETag, 秒级)
                   │
        ┌──────────┴───────────┐
        ▼          ▼           ▼
   ┌────────┐ ┌────────┐  ┌────────┐
   │数据面1 │ │数据面2 │  │数据面N │   Go, 无状态横向扩展
   │ +内存  │ │ +内存  │  │ +内存  │   限流/缓存默认内存实现
   └───┬────┘ └───┬────┘  └───┬────┘   （多实例需精确全局限流时接 Redis）
       └──────────┴───────────┘         - 协议适配 / 插件链 / 流式转发

> **Superseded by ADR-0013**：数据面非纯无状态——持有配额 PG 直连做 pre-debit/settle（fail-closed）。
                  │
                  ▼
        ┌─────────────────────┐
        │  上游模型供应商      │
        │ OpenAI/Claude/腾讯/  │
        │ 智谱/OpenAI兼容API   │
        └─────────────────────┘
```

部署演进：**阶段一传统/VM 部署**（systemd + 二进制 + PG；限流/缓存走内存）→ **阶段二 K8s**（Helm chart，数据面 Deployment 横向扩展，按需引入 Redis 做全局限流）。

### 3.2 数据面请求处理流程

```
请求进入
  │
  ▼
[1] API Key 鉴权 ──────────────► 失败 401
  │
  ▼
[2] 租户识别 + 配额检查 ───────► 超额 429
  │
  ▼
[3] 限流（内存令牌桶，默认）───► 触发 429
  │
  ▼
[4] 前置插件链（pre）
    - 敏感词检查
    - 请求改写（expr 规则）
    - 缓存命中检查 ──────────► 命中则直接返回
  │
  ▼
[5] 路由决策
    - 按模型名映射到供应商
    - 负载均衡 / 故障切换选实例
  │
  ▼
[6] 协议适配（adapter）
    - OpenAI 兼容请求 → 供应商原生请求
  │
  ▼
[7] 转发到上游（支持流式 SSE）
    - 熔断器保护
    - 失败按策略重试/降级到备用供应商
  │
  ▼
[8] 响应适配
    - 供应商响应 → OpenAI 兼容格式
    - 流式聚合 token 用量
  │
  ▼
[9] 后置插件链（post）
    - 审计日志
    - 用量计费（usage 入账）
    - 响应改写
  │
  ▼
返回业务方（流式逐块返回）
```

### 3.3 供应商适配器抽象

首批支持：**OpenAI、Claude (Anthropic)、腾讯混元、智谱 GLM、任意 OpenAI 兼容 API**。

```go
// Adapter 将统一的 OpenAI 兼容请求适配到具体供应商。
// 纯翻译：值进值出，不做 HTTP 传输/超时/重试（那是 proxy 的职责），便于用
// testdata 样本做表驱动测试。
type Adapter interface {
    Name() string
    // 将统一请求转为传输中立的上游请求描述（proxy 再转成 *http.Request 发送）
    BuildRequest(ctx context.Context, req *UnifiedRequest) (*UpstreamRequest, error)
    // 解析上游非流式响应体（字节）为统一格式
    ParseResponse(body []byte) (*UnifiedResponse, error)
    // 解析上游流式响应体（io.Reader, SSE），逐块转为统一格式 chunk
    ParseStream(body io.Reader) (StreamReader, error)
    // 从响应中提取 token usage（计费用）
    ExtractUsage(resp *UnifiedResponse) (*Usage, error)
}
```

- OpenAI / 智谱 / 腾讯混元 / 其他兼容 API：大多遵循 OpenAI 协议，可共用 `OpenAICompatibleAdapter`，差异通过配置（base_url、鉴权头、模型名映射）处理
- Claude：协议差异较大（messages 结构、SSE 事件类型不同），单独 `ClaudeAdapter`

### 3.4 插件机制（分层策略）

| 插件类型 | 实现方式 | 示例 | 阶段 |
|---|---|---|---|
| 协议适配 | 内置 Go interface + 注册 | 新增供应商 | P0 |
| 治理规则 | `expr-lang/expr` 表达式 | 路由规则、敏感词判断、请求改写 | P0 |
| 内置治理插件 | Go 实现 | 限流、审计、缓存、配额 | P0 |
| 用户自定义复杂插件 | WASM (`wazero`) | 第三方扩展 | P1（预留入口） |

插件链接口：
```go
type Plugin interface {
    Name() string
    Phase() Phase // Pre / Post
    Execute(ctx *PluginContext) error // 可中断链路（如缓存命中、敏感词拦截）
}
```

---

## 4. 客户端 SDK 设计（TypeScript，薄封装）

### 4.1 定位

在 OpenAI 官方 TS SDK 之上做薄封装，**调用风格与 OpenAI SDK 保持一致**，业务方迁移成本最低。封装的企业特有能力：

- 自动注入企业 API Key 与网关 base_url
- 统一鉴权（企业 token → 网关 API Key）
- 供应商无关的模型路由（业务方只写模型别名）
- 用量上报 / 配额查询接口
- 统一错误码与重试策略

### 4.2 接口草图

```typescript
import { VoxeltoadGateway } from '@voxeltoad/gateway-sdk';

const client = new VoxeltoadGateway({
  apiKey: process.env.VOXELTOAD_API_KEY,
  baseURL: 'https://gateway.internal.company.com/v1', // 可省略，走默认
});

// 调用风格与 OpenAI SDK 完全一致
const stream = await client.chat.completions.create({
  model: 'gpt-4o',        // 或别名 'default-chat'，由网关路由
  messages: [{ role: 'user', content: 'hello' }],
  stream: true,
});
for await (const chunk of stream) { /* ... */ }

// 企业扩展能力
const quota = await client.usage.getQuota();      // 配额查询
const models = await client.models.list();        // 可用模型列表
```

### 4.3 测试集合（核心交付物）

SDK 测试同时作为**网关 API 的契约/回归测试**：

1. **契约测试** — 基于网关 OpenAPI 定义，校验 SDK 请求/响应符合契约
2. **集成测试** — 启动 mock 上游（mock OpenAI/Claude/腾讯/智谱）→ SDK 调真实网关 → 验证全链路
   - 非流式 / 流式（SSE）两套
   - 鉴权失败、限流、配额超限、熔断降级等异常路径
   - 多供应商路由、故障切换
3. **用量与计费校验** — 验证 usage 字段正确入账
4. CI 中 SDK 测试套件作为网关每次变更的回归门禁

---

## 5. 数据模型（管理面核心表，PostgreSQL）

| 表 | 说明 |
|---|---|
| `tenants` | 租户 |
| `api_keys` | API Key（哈希存储），关联租户、权限范围 |
| `providers` | 供应商配置（base_url、鉴权、超时、权重） |
| ~~`provider_credentials`~~ | ~~供应商密钥（加密存储）~~ **已 defer（ADR-0014:7-9）**：当前 `providers.spec.api_key_ref` 存 ADR-0003 引用串（`env://VAR`），加密落库待新 ADR |

> **Superseded by ADR-0031**：AES-256-GCM 已落地（migration 00009，`db://provider/<name>` scheme）。
| `models` | 模型定义（别名、映射的供应商模型、定价） |
| `routes` | 路由规则（模型别名 → 供应商列表 + 策略） |
| `quotas` | 配额（租户/Key 维度，token/请求数/金额） |
| `usage_records` | 用量记录（计费明细，可按需归档） |
| `plugins` | 插件配置（类型、参数 JSONB、启用状态、作用域） |

> 完整表清单（含 `groups`/`operators`/`sessions`/`audit_logs`/`request_logs`/`config_generation`）与 ER 图见 [design/database.md](../../design/database.md)。本表仅为初始设计快照。
| `audit_logs` | 审计日志 |

配置类数据（providers/models/routes/plugins）由管理面写入 PG，数据面轮询管理面的配置快照接口（`/internal/config/snapshot`，version/ETag 条件请求）拉取并原子替换到内存热更新。

---

## 6. 项目结构（monorepo）

```
voxeltoad/
├── cmd/
│   ├── gateway/         # 数据面入口
│   └── admin/           # 管理面入口
├── internal/
│   ├── proxy/           # 数据面核心：路由、转发、流式处理
│   ├── adapter/         # 供应商适配器 (openai/claude/tencent/zhipu/compatible)
│   ├── plugin/          # 插件框架 + 内置插件（限流/审计/敏感词/缓存/配额）
│   ├── admin/           # 管理面 API + 服务层
│   ├── config/          # config.go（bootstrap）/ poller.go（快照轮询）
│   ├── billing/         # token 计费/配额
│   ├── auth/            # API Key/JWT
│   ├── store/           # PG 持久化 (gorm)
│   └── observability/   # OTel/metrics/logging
├── pkg/                 # 可对外暴露的少量库
├── api/                 # OpenAPI / protobuf 定义（SDK 与前端的单一事实来源）
├── sdk/
│   └── typescript/      # TS 薄封装 SDK + 测试集合
├── web/                 # React 管理面前端（P1）
├── deploy/
│   ├── vm/              # systemd unit / 二进制部署脚本（阶段一）
│   └── helm/            # Helm chart（阶段二 K8s）
├── test/                # 集成测试 / mock 上游
└── docs/
```

> **Superseded by 实现**：`api/` 目录实际只剩 README，真实 OpenAPI 定义在 `docs/openapi/admin.yaml`。

---

## 7. 已识别的风险与应对

| 风险 | 应对 |
|---|---|
| 管理面 CRUD 开发速度不如 Java/Python | 用 gin+gorm，不上重型框架；UI 推迟到 P1 |
| Go 插件机制选型内耗 | 分层策略已定：Go interface（适配）+ expr（规则）+ WASM（P1 扩展） |
| Token 计费精度（tokenizer 差异） | 以上游返回的 usage 为准，本地 tokenizer 仅预估 |
| 各供应商流式 SSE 事件格式不一致 | adapter 层统一为内部 chunk 模型，Claude 单独适配 |
| 配置热更新一致性 | HTTP 快照轮询 + 内存原子替换（atomic.Pointer），避免重启 |
| LLM 语义可观测性缺失 | OTel 自定义 attribute：模型名/prompt&completion token/TTFT/供应商/是否缓存/是否拦截 |

---

## 8. 实现路线（建议顺序）

1. **基础骨架** — monorepo 结构、配置加载、PG 接入（本地 embedded-postgres）、配置快照轮询、OTel 初始化
2. **数据面 MVP** — OpenAI 兼容代理 + OpenAICompatibleAdapter + SSE 流式转发（先打通一个供应商）
3. **鉴权与限流** — API Key 鉴权、内存令牌桶限流（Redis 实现留多实例扩展）
4. **多供应商** — Claude / 腾讯混元 / 智谱适配器 + 路由 + 故障切换
5. **治理能力** — 配额/计费、审计、敏感词、缓存插件
6. **管理面 API** — providers/models/routes/keys/quotas CRUD + 配置快照接口
7. **TS SDK + 测试集合** — 薄封装 SDK + 契约/集成测试（CI 门禁）
8. **VM 部署方案** — systemd + 部署脚本 + 文档
9. **（P1）React 管理 UI / WASM 插件 / K8s Helm / Redis 限流后端**

---

## 附：关键决策记录

- 数据面/管理面同构 Go —— 避免双技术栈，团队倾向 Go
- 管理面轻量，非卡点 —— 不引入 CRD/Operator 复杂度
- 部署先 VM/传统，后 K8s
- 供应商首批：OpenAI + Claude + 腾讯 + 智谱 + OpenAI 兼容 API
- SDK：TypeScript，薄封装定位
- API 优先，前端 React 推迟
- 测试以 SDK 测试集合为核心，兼作网关回归门禁
