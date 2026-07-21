# Database

> 管理面 PostgreSQL schema 的可视化与集中清单。ADR-0014 是决策源，本文件是单一事实来源（可视化 + 完整表清单 + 软引用关系 + 设计决定说明）。
> **适用对象**：修改 schema、新增表、调整跨表引用关系时，先读本文件对齐全貌，再查相关 ADR 看决策背景。

Schema 形状由 `internal/store/migrations/` 下的 24 个 goose 迁移定义（`00001`-`00025`，缺 `00018`）；本文件与之保持同步。其中 `00018` 因 trace 迁移重命名而跳过（见 git log `856613d`）。

> **同步规则（门禁）**：任何 PR 若新增/修改/删除表、列、索引或约束（即触碰 `internal/store/migrations/` 下的 SQL 文件），**必须同步更新本文件**。检查清单：
> - §1 表分类矩阵：新表归入正确分类，业务表计数同步
> - 头部迁移数量（"23 个 goose 迁移"）同步
> - 对应表的 ER 图块：新列/新索引加进去
> - §3.1（及类似专节）字段表、索引清单、字段计数同步
> - §3.2（及类似对比表）字段数同步
> - 必要时在 §5 追加设计决定说明，或开新 ADR 记录决策背景
>
> 本文件自称单一事实来源——保持同步是 PR 作者的责任，不是 reviewer 的责任。

## §1 概览

### 表分类矩阵

21 张业务表 + 2 张 goose 隐式表，按作用域分 4 类：

| 分类 | 表 | RBAC 边界 | 备注 |
|---|---|---|---|
| **全局资源** | `providers` `models` `routes` `plugins` `config_snapshots` `data_plane_nodes` `provider_credentials` `gateway_settings` | super-admin (wildcard) 管理；支持自定义角色通过 `requirePermission()` 授权 | 平台级，无 `tenant_id`，所有租户共享 |
| **租户作用域** | `tenants` `groups` `api_keys` `quotas` `usage_records` `request_logs` `trace_payloads` | tenant-admin 管自己；super-admin 管所有；自定义租户角色按 permissions 授权 | 三级层级 Tenant→Group→APIKey（ADR-0005） |
| **运营** | `operators` `sessions` `audit_logs` `roles` `role_permissions` | super-admin 管所有 operators 和 roles；各 operator 管自己 sessions | 邮箱+密码登录，与 client API Key 是两套系统 |
| **元数据** | `config_generation` `goose_db_version` | 系统内部，无直接 API | 两条独立版本线（ADR-0015） |

### RBAC 边界速查

```
角色 → 权限集 (role_permissions) + scope_kind (global | tenant)
  - super-admin: scope=global, permissions="*" (通配)
  - tenant-admin: scope=tenant, permissions=api_key/group/usage/audit/request_log/quota.read + password.write
  - 自定义角色: 可 free 创建，勾选任意 permission，声明 global 或 tenant scope

全局 scope 角色 (scope_kind='global'):
  - tenant_id IS NULL（不绑定租户）
  - 需 requireTenantScoped() 中间件拒绝访问租户路由
  - 典型权限: providers/models/routes/plugins CRUD, tenants/operators CRUD, quota write, roles 管理

租户 scope 角色 (scope_kind='tenant'):
  - tenant_id IS NOT NULL（绑定一个租户）
  - 通过 scoped-repository 结构性隔离（TenantRepo 构造时绑定 tenantID）
  - 典型权限: api_keys/groups CRUD, usage/audit/request-log/quota 只读

隔离是结构性的：`TenantRepo` 构造时绑定 `tenantID`，无方法指定其他租户（`internal/store/tenant.go:8-23`）。
权限校验通过 `requirePermission(perm)` 中间件 + `operator.Permissions` 集合。
```

---

## §2 全局配置表 ER 图

```mermaid
erDiagram
    providers ||..o{ models : "spec.upstreams[].provider (软引用)"
    providers ||..o{ routes : "spec.providers[].name (软引用)"
    models ||..|| routes : "model_alias → alias (软引用)"
    config_generation }|..|| providers : "version bump on write"
    config_generation }|..|| models : "version bump on write"
    config_generation }|..|| routes : "version bump on write"
    config_generation }|..|| plugins : "version bump on write"
    providers ||--o| provider_credentials : "provider_name FK CASCADE"
    config_generation }|..|| gateway_settings : "version bump on write"

    providers {
        bigint id PK
        varchar name UNIQUE "唯一标识/实例名"
        varchar type "品牌: openai/tencent/zhipu/anthropic"
        varchar adapter "协议键: openai|claude"
        boolean enabled
        jsonb spec "完整 Provider 结构"
        timestamptz created_at
        timestamptz updated_at
    }

    models {
        bigint id PK
        varchar alias UNIQUE "客户端请求的别名"
        boolean enabled
        jsonb spec "Alias + Upstreams[](provider/upstream_model/pricing)"
        timestamptz created_at
        timestamptz updated_at
    }

    routes {
        bigint id PK
        varchar model_alias UNIQUE "指向 models.alias"
        varchar strategy "priority|weighted|round_robin|session_affinity"
        boolean enabled
        jsonb spec "ModelAlias + Providers[](name/weight) + Strategy"
        timestamptz created_at
        timestamptz updated_at
    }

    plugins {
        bigint id PK
        varchar name "无 UNIQUE — 用 (name,scope) 应用层唯一键"
        varchar phase "pre|post"
        varchar scope "tenant/model/空=global"
        boolean enabled
        jsonb spec "PluginConfig"
        timestamptz created_at
        timestamptz updated_at
    }

    config_generation {
        bigint version "单行表，ETag 计数器"
    }
```

### `spec` JSONB 内容

每张 config 表的 `spec` 列存整个 Go 结构体的 JSON 序列化（`internal/config/schema.go`）：

- `providers.spec` → `config.Provider`（Name/Type/Adapter/BaseURL/APIKeyRef/Timeouts/Weight）
- `models.spec` → `config.Model`（Alias + `Upstreams[]`，每个 upstream 含 Provider/UpstreamModel/DefaultMaxTokens/Pricing）
- `routes.spec` → `config.Route`（ModelAlias + `Providers[].{Name,Weight}` + Strategy）
- `plugins.spec` → `config.PluginConfig`（Name/Phase/Params/Enabled/Scope）

身份列（`name`/`alias`/`model_alias`）是纯反范式，用于 UNIQUE 约束和查询；leaf detail 在 spec 里。**加字段不需要 SQL migration**（ADR-0014 §1）。

### 关键约束

- `Route.Providers ⊆ Model.Upstreams.Provider`：admin 写时校验，不满足 → 400（见 §5.1）
- `plugins` 无 UNIQUE 约束，用 `(name, scope)` 应用层唯一键 + DELETE+INSERT upsert（见 §5.2）

### §2.1 新增全局表：`config_snapshots`（ADR-0025）

每次配置变更（provider/model/route/plugin Upsert 或 Delete）异步保存完整 `config.Dynamic` 快照，支持版本历史浏览、差异对比与回滚。

| 列 | 类型 | 说明 |
|---|---|---|
| `version` | BIGINT PK | 对应 `config_generation.version` |
| `payload` | JSONB | 完整快照（`config.Dynamic` 序列化） |
| `created_at` | TIMESTAMPTZ | 快照保存时间 |

### §2.2 新增全局表：`data_plane_nodes`（ADR-0024）

数据面实例自注册与心跳清单。纯可观测性用途，不用于服务发现（路由走 Ingress/LB）。

| 列 | 类型 | 说明 |
|---|---|---|
| `instance_id` | TEXT UNIQUE | `{hostname}-{pid}` |
| `hostname` / `addr` / `version` / `commit` | TEXT | 实例元信息 |
| `config_generation` | BIGINT | 当前加载的配置版本 |
| `status` | TEXT | online / draining / offline |
| `last_heartbeat_at` | TIMESTAMPTZ | 心跳时间，超 45s 判死 |
| `breaker_states` | JSONB | per-instance 熔断态（B2'） |

### §2.3 `provider_credentials`（加密落库，ADR-0031）

上游 provider 凭证的加密存储。`providers.spec.api_key_ref` 仍存 ADR-0003 引用串，新增 `db://provider/<name>` scheme 指向本表加密行，与 `env://VAR` / `plain://literal` / 裸字面量并存。明文仅存于进程内存，绝不落库、快照或日志。设计决定背景见 §5.5。

| 列 | 类型 | 说明 |
|---|---|---|
| `provider_name` | VARCHAR PK | FK `REFERENCES providers(name) ON DELETE CASCADE` |
| `ciphertext` | BYTEA NOT NULL | AES-256-GCM 密文 |
| `nonce` | BYTEA NOT NULL | GCM nonce |
| `algorithm` | VARCHAR NOT NULL DEFAULT `'AES-256-GCM'` | 加密算法 |
| `key_version` | VARCHAR NOT NULL DEFAULT `'v0'` | 密钥版本，支持轮换 |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

**关键设计**：`FK ... ON DELETE CASCADE` 是全库**唯一两个**带 CASCADE 的 FK 之一（另一个是 `sessions.operator_id`）。provider 删除时凭证级联消失，不留孤儿。凭证经专用 `PATCH /api/v1/providers/{name}/credential` 端点轮换，**无需重启数据面**（dispatcher 重建时解密取用）。

### §2.4 `gateway_settings`（单行 JSONB 热重载设置）

网关行为参数的全局容器，单行表设计。承载热重载参数——admin 面写入，数据面通过 config snapshot poll 读取（~5s 生效，无需重启）。设计决定背景见 §5.9。

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | SMALLINT PK DEFAULT 1 | `CHECK id = 1`（单行表） |
| `spec` | JSONB NOT NULL DEFAULT `'{}'` | 完整 `config.GatewaySettings` 序列化 |
| `updated_at` | TIMESTAMPTZ | |

**关键设计**：单行 + JSONB——**加字段不需要 SQL migration**（同 config 表家族，spec 自动 round-trip）。每次写入同一事务 bump `config_generation`，触发数据面 snapshot 重载。

**目前 spec 内容**（源自 ADR-0039 §2 的热重载要求）：`{ trace: { capture_payload_enabled, max_body_kb, retention_days } }`。未来 `otel` / `rate_limit` 等参数会加在这里。该表无独立 ADR——它作为 ADR-0039 热重载需求的载体表引入。

**读 API**：`GET /api/v1/gateway-settings`（读）、`PUT /api/v1/gateway-settings`（整体替换，super-admin only，写入被审计）。

---

## §3 租户作用域表 ER 图

```mermaid
erDiagram
    tenants ||--o{ groups : "tenant_id FK"
    tenants ||--o{ api_keys : "tenant_id FK"
    groups ||--o{ api_keys : "group_id FK (nullable)"
    tenants ||..o{ quotas : "scope='tenant:<name>' (软引用)"
    groups ||..o{ quotas : "scope='group:<name>/<group>' (软引用)"
    api_keys ||..o{ quotas : "scope='key:<id>' (软引用)"
    models ||..o{ api_keys : "allowed_models[] → alias (软引用,可选校验)"
    tenants ||..o{ usage_records : "tenant (反范式,不校验)"
    tenants ||..o{ request_logs : "tenant (反范式,不校验)"
    tenants ||..o{ trace_payloads : "tenant (反范式,不校验)"
    trace_payloads }o..|| request_logs : "request_id 1:1 应用层 join (非 SQL FK)"

    tenants {
        bigint id PK
        varchar name UNIQUE
        boolean enabled "存在但无端点 toggle (P2 缺口)"
        timestamptz created_at
        timestamptz updated_at
    }

    groups {
        bigint id PK
        bigint tenant_id FK "NOT NULL"
        varchar name
        boolean enabled
        timestamptz created_at
        timestamptz updated_at
        unique "UNIQUE(tenant_id, name)"
    }

    api_keys {
        bigint id PK
        varchar key_id UNIQUE "公开标识"
        char hash UNIQUE "SHA-256 hex, 64 字符"
        bigint tenant_id FK "NOT NULL"
        bigint group_id FK "NULLABLE, 可无 group"
        timestamptz expires_at
        jsonb allowed_models "JSONB, 空=全部允许"
        timestamptz revoked_at "软删"
        timestamptz created_at
    }

    quotas {
        varchar scope PK "扁平串: tenant:X / group:X/Y / key:Z / 裸串"
        bigint balance "微单位 (ADR-0013)"
        varchar currency
        timestamptz updated_at
    }

    usage_records {
        bigint id "PK(id, created_at)"
        varchar tenant "反范式,无 FK"
        varchar group_name "反范式"
        varchar api_key_id "反范式"
        varchar provider
        varchar model
        integer prompt_tokens
        integer completion_tokens
        integer cached_prompt_tokens "cache 读命中的 prompt (00017)"
        bigint cost "微单位"
        bigint cache_discount_micros "cache 命中折扣 (00017)"
        varchar request_id "gateway 关联 ID (00014)"
        varchar session_id "X-Voxeltoad-Session (00014)"
        varchar trace_id "W3C trace id (00015)"
        timestamptz created_at "PARTITION BY RANGE"
    }

    request_logs {
        bigint id "PK(id, created_at)"
        varchar tenant "反范式,无 FK"
        varchar group_name
        varchar api_key_id
        varchar provider
        varchar model_requested
        varchar model_resolved
        boolean stream
        integer prompt_tokens
        integer completion_tokens
        integer total_tokens
        integer ttft_ms "Time To First Byte"
        integer duration_ms
        varchar error_type
        varchar blocked_by "拦截插件名"
        boolean fallback "是否发生了 failover"
        varchar request_id "gateway 分配/上行透传"
        varchar session_id "X-Voxeltoad-Session header"
        varchar trace_id "W3C trace id (00015)"
        varchar session_source "session key 来源 (00016)"
        boolean cache_hit "上游 prompt cache 命中 (00017)"
        varchar cache_tier "upstream | gateway(预留)"
        varchar cache_source "cache 来源 provider 名"
        integer cached_prompt_tokens "cache 读命中 token 数"
        varchar agent_type "claude-code/codex/... (00023)"
        text    upstream_request_id "provider 返回的请求 ID (00024)"
        varchar ingress_protocol "openai|anthropic (00025)"
        timestamptz created_at "PARTITION BY RANGE"
    }

    trace_payloads {
        bigint id "PK(id, created_at)"
        varchar request_id "应用层 join request_logs (非 FK)"
        varchar session_id "X-Voxeltoad-Session header"
        varchar trace_id "W3C trace id"
        varchar tenant "反范式,无 FK"
        varchar group_name
        varchar api_key_id
        varchar provider
        varchar model_requested
        boolean stream
        varchar agent_type "claude-code/codex/... (00023)"
        varchar ingress_protocol "openai|anthropic (00025)"
        integer status_code "上游 HTTP 状态"
        varchar stop_reason "finish/stop reason"
        integer n_messages
        integer n_tool_use
        jsonb messages "归一化 adapter.Message[]"
        jsonb request_raw "原始 client 请求体"
        text response_raw "上游响应体（流式为重装 SSE 转录）"
        text error_raw "上游错误体"
        timestamptz created_at "PARTITION BY RANGE"
    }
```

### §3.1 `request_logs` 专节

**用途**：每条 LLM 请求（成功或拒绝）追加一行的数据面审计账本。与 `audit_logs`（管理面配置变更审计）是**两个不同系统**（`migrations/00004_request_logs.sql:4-6`）。

**字段语义**（重点字段与 `design/observability.md` 语义字段对齐）：

| 字段 | 语义 | 来源 |
|---|---|---|
| `tenant` / `group_name` / `api_key_id` | 反范式身份串，保留历史 | auth 记录（ADR-0006） |
| `provider` | 实际命中的上游 provider | dispatcher 返回值 |
| `model_requested` | 客户端请求的 alias | `llm.model.requested` |
| `model_resolved` | 路由解析后的 provider-native 名 | `llm.model.resolved` |
| `stream` | 是否流式请求 | UnifiedRequest.Stream |
| `prompt_tokens` / `completion_tokens` / `total_tokens` | token 用量 | adapter.ExtractUsage |
| `ttft_ms` | Time To First Byte（毫秒） | 数据面计时 |
| `duration_ms` | 总耗时（毫秒） | 数据面计时 |
| `error_type` | 错误类型（`permission_error`/`rate_limit_error`/`api_error` 等） | router 拒绝状态映射 |
| `blocked_by` | 拦截该请求的插件名（空=未被拦截） | plugin.Context.BlockedBy |
| `fallback` | 是否发生了 failover | dispatcher 命中 provider 与首选不一致 |
| `request_id` | gateway 分配（或上行透传）的请求关联 ID | entry middleware / 上行 header |
| `session_id` | 客户端传入的会话 key | `X-Voxeltoad-Session` header |
| `trace_id` | W3C trace id（从 traceparent 解析，空=无/无效） | entry middleware（00015） |
| `session_source` | session key 来源标签（header-config/body-session/body-metadata/body-user/prefix） | session 检测中间件（00016） |
| `cache_hit` | 上游 prompt cache 是否命中 | adapter usage + dispatcher（00017） |
| `cache_tier` | 缓存层级（`upstream` v1；预留 `gateway`） | dispatcher（00017） |
| `cache_source` | 缓存来源 provider 名 | dispatcher（00017） |
| `cached_prompt_tokens` | cache 读命中的 prompt token 数 | adapter usage（00017） |
| `agent_type` | 检测到的调用 agent（claude-code/codex/codebuddy/workbuddy/opencode；空=未识别） | agent 检测中间件（00023） |
| `upstream_request_id` | provider 返回的请求关联 ID（OpenAI `x-request-id` 头、Anthropic `request-id` 头/body 等），仅最终成功尝试 | Forwarder 从 `resp.Header` 提取（00024） |
| `ingress_protocol` | 客户端入站协议（`openai` / `anthropic`；空=迁移前历史行），驱动管理面协议筛选与直通/转换 badge | 数据面 codec.Protocol()（00025） |

**如何关联链路**: `GET /api/v1/request-logs?session_id=X` 查询同一 session 的所有请求；`request_id` 用于精确定位单次请求并与 OTel trace 串联；`upstream_request_id` 用于售后/对账时定位到 provider 侧的请求记录（按上游 ID 反查网关请求，见索引自 `idx_request_logs_upstream_request_id`）。

**写入时机**：数据面异步 fail-open 记录（ADR-0016），不阻塞请求路径。写入失败只记日志不报错。

**分区策略**：`PARTITION BY RANGE (created_at)` + `request_logs_default` 默认分区 + 月度分区（`00007_request_logs_monthly_partitions`），支持 partition-DROP TTL（ADR-0029）。

**索引**：`idx_request_logs_created_at`、`idx_request_logs_tenant_created`、`idx_request_logs_session_created`（00010，按 session_id 查询）、`idx_request_logs_trace_id`（00015，按 trace 串联）、`idx_request_logs_upstream_request_id`（00024，按上游 ID 反查）。

**读 API**：`GET /api/v1/request-logs`（offset 分页，支持 tenant/provider/model/error_type/session_id/request_id/upstream_request_id 等过滤 + CSV 导出）、`GET /api/v1/request-logs/sessions`（按 session 聚合）、`GET /api/v1/request-logs/sessions/:session_id`（session 内请求时间线）。RBAC 隔离：super-admin 全局视图，租户角色 scoped。

### §3.2 `usage_records` 与 `request_logs` 的区别

| 维度 | `usage_records` | `request_logs` |
|---|---|---|
| 用途 | 计费/对账 | 审计/合规 |
| 写入时机 | billing plugin 结算阶段 | 请求结束时异步 |
| 字段数 | 15 列 | 27 列 |
| cost 字段 | 有（微单位 + cache 折扣） | 无 |
| token 字段 | prompt/completion + cached_prompt_tokens | prompt/completion/total + cached_prompt_tokens |
| 性能字段 | 无 | ttft_ms/duration_ms/error_type/blocked_by/fallback |
| 缓存维度 | cache_discount_micros（折扣） | cache_hit/cache_tier/cache_source（命中详情） |
| 关联 ID | request_id/session_id/trace_id | request_id/session_id/trace_id/upstream_request_id |
| agent 检测 | 无 | agent_type |

两表都有 `tenant`/`group_name`/`api_key_id`/`provider` 反范式身份串，无 FK（ADR-0014:118-122：append-only 审计行应保留身份原样，不受后续重命名/删除影响）。

### §3.3 `quotas.scope` 命名约定

扁平字符串，非 FK。约定（`design/domain-flows.md:98,135`）：

| scope 形态 | 示例 | 校验 |
|---|---|---|
| `tenant:<name>` | `tenant:acme` | topup 时校验 tenant 存在→400 |
| `group:<name>/<group>` | `group:acme/team-a` | **不校验存在**（允许预充值） |
| `key:<id>` | `key:ak-123` | **不校验存在**（允许预充值） |
| 裸串 | `custom-budget` | 无前缀，纯自定义 |

三级层级对应 ADR-0005 的 hierarchical ceilings（key→group→tenant 独立扣减，LiteLLM 模型）。

### §3.4 `trace_payloads`（4 层 trace 模型的底部两层，ADR-0039）

每条 LLM 请求的 prompt/completion 正文 + 原始请求/响应体。与 `request_logs` 是**两个独立账本**——`request_logs` 只存元数据（ADR-0021 §2 明确禁止存正文），`trace_payloads` 专门承载 4 层 trace 模型（Session → Request → Messages → Raw）的底部两层。两者按 `request_id` 在**应用层** 1:1 配对（**非 SQL JOIN**）。分离原因见 §5.8。

**字段语义**：

| 字段 | 语义 |
|---|---|
| `messages` (JSONB) | 归一化的 `adapter.Message[]`，provider 无关的消息层 |
| `request_raw` (JSONB) | 原始 client 请求体（handler 已读一次后复用） |
| `response_raw` (TEXT) | 上游响应体；非流式是原文（`UnifiedResponse.Raw`），流式是重装的完整 SSE 转录（ADR-0032） |
| `error_raw` (TEXT) | 上游错误体（目前只发给 client 后丢失，此处保留） |
| `status_code` / `stop_reason` / `n_messages` / `n_tool_use` | summary 维度，让列表视图不解析大 JSON 就能渲染 |
| `agent_type` | 检测到的调用 agent（00023 后加） |
| `ingress_protocol` | 客户端入站协议（00025 后加，`openai`/`anthropic`/空） |
| `request_id` / `session_id` / `trace_id` / `tenant` / `group_name` / `api_key_id` | 与 `request_logs` 相同的关联身份串（应用层配对） |

**捕获开关**：默认**关**（`gateway_settings.trace.capture_payload_enabled`），热重载（~5s 生效，无需重启）。关闭时捕获方法短路，零成本（不拷贝 body、不 marshal）。

**写入时机**：异步 fail-open（`AsyncTracePayloadRecorder`，ADR-0016 模式），不阻塞请求路径；buffer 满丢弃并计数 `trace_payloads_dropped_total`。丢弃可接受——trace payload 是调试态，不是 money path。

**分区策略**：`PARTITION BY RANGE (created_at)` + 默认分区 + 月度分区（`00020_trace_payloads_monthly_partitions`），partition-DROP TTL（默认 7 天，ADR-0039 §4）。DROP 是 O(1) 操作，避免 DELETE 扫描大 JSONB 行。

**索引**：`idx_trace_payloads_request_id`（按 request_id 点查/配对）、`idx_trace_payloads_session_created`（session 视图）、`idx_trace_payloads_tenant_created`（租户列表）。

**读 API**：`GET /api/v1/trace/sessions/:session_id`（session 内 trace 列表）、`GET /api/v1/trace/requests/:request_id`（单请求详情，按 request_id）、`GET /api/v1/trace/rows/:id`（按自增主键点查，应对 request_id 重复场景）。**读访问被审计**（ADR-0039 §5，读 prompt/completion 明文是敏感操作，每次详情读都写 `audit_logs` 行）。

**保留期**：默认 7 天，partition-DROP 兜底。与 `request_logs`（长期保留）策略**故意不同**——这正是两表分离的核心原因（见 §5.8）。

**`response_raw` 类型历史**：ADR-0039:158 声明为 TEXT，但 `00019` 实际创建为 JSONB，`00022` 修正为 TEXT。ADR 描述的是 `00022` 之后的最终态，未标注中间修正。

---

## §4 运营表 ER 图

```mermaid
erDiagram
    tenants ||--o{ operators : "tenant_id FK (nullable, global=NULL)"
    operators }o--|| roles : "role_id FK"
    roles ||--o{ role_permissions : "role_id FK ON DELETE CASCADE"
    operators ||--|| sessions : "operator_id FK ON DELETE CASCADE"
    operators ||..o{ audit_logs : "operator_id (无 FK, 历史完整性优先)"
    tenants ||..o{ audit_logs : "tenant (软引用, ADR-0019)"

    operators {
        bigint id PK
        varchar email UNIQUE
        varchar password_hash "argon2id"
        varchar role "legacy text, 保留用于回滚 (PHASE-2)"
        bigint role_id FK "NOT NULL → roles.id"
        bigint tenant_id FK "NULLABLE, global=NULL"
        timestamptz created_at
        timestamptz updated_at
    }

    roles {
        bigint id PK
        varchar name UNIQUE
        varchar scope_kind "CHECK: global|tenant"
        boolean is_builtin "true=不可删除"
        varchar description
        timestamptz created_at
        timestamptz updated_at
    }

    role_permissions {
        bigint role_id PK FK "ON DELETE CASCADE"
        varchar permission PK "resource.action 格式; * 为通配"
    }

    sessions {
        varchar token PK "opaque, 可即时撤销"
        bigint operator_id FK "ON DELETE CASCADE"
        timestamptz expires_at
        timestamptz created_at
    }

    audit_logs {
        bigint id PK
        bigint operator_id "无 FK, 保留历史"
        varchar action
        varchar resource_type
        varchar resource_id
        jsonb before "phase-1 未使用 (见 §5.4)"
        jsonb after "phase-1 只写 after"
        varchar tenant "00003 后加, ADR-0019"
        timestamptz created_at
    }
```

### `operators.role` 与 `operators.role_id`（Phase-2 RBAC）

- **`role` 列**（VARCHAR）：phase-1 遗留文本列，创建 operator 时仍然写入，保留用于回滚。原列级 CHECK `role IN ('super-admin','tenant-admin')` 与表级 `operator_role_tenant` CHECK 均已由迁移 `00013_operators_role_check.sql` 删除（自定义角色落地后，DB 约束无法枚举 `roles` 表中的角色名）。
- **`role_id` 列**（BIGINT NOT NULL REFERENCES roles(id)）：phase-2 新增，指向 `roles` 表。`OperatorRepo.Create` 通过子查询 `(SELECT id FROM roles WHERE name = ?)` 解析 role 名得到 role_id。
- **`scope_kind` 一致性现由应用层校验**：`(role.scope_kind = 'global' AND tenant_id IS NULL) OR (role.scope_kind = 'tenant' AND tenant_id IS NOT NULL)` 由 handler 边界保证（`mountOperators` 检查 `roles.scope_kind ↔ tenant_id`）。DB 不再有该 CHECK 约束。

**内置角色**（seed 于 00011 迁移）:
| name | scope_kind | is_builtin | permissions |
|---|---|---|---|
| super-admin | global | true | `*`（通配，所有权限） |
| tenant-admin | tenant | true | api_key.read/write, group.read/write, usage.read, audit.read, request_log.read, quota.read, password.write |

> **models 特例**：`GET /api/v1/models` 不查 `model.read` 权限点，走「仅认证」放行（server.go 的 `configReadGrp`），因为 models 是全局共享配置且 API-key 表单需读别名。故 tenant-admin 虽无 `model.read` 权限点仍可 GET models——这是**有意**的读开放/写关闭 carve-out（见 `crud_model.go` 头注释）。其余全局配置（providers/routes/plugins/operators/tenants）的 GET 挂 `globalGrp`，tenant-admin → 403。

### `role_permissions` 编码规则

- permission 格式为 `resource.action`（如 `provider.write`），全小写点分。
- `*` 为通配哨兵值，表示"所有权限"——内置 super-admin 角色只拥有这一行。
- permission catalog 定义在 `internal/authz/permission.go`，是 Go 常量清单（不建 permissions 表），与 `internal/apperr` 同构。

**历史记录：`operator_role_tenant` CHECK 约束已移除**

phase-1 的表级约束 `operator_role_tenant`（及列级 `CHECK (role IN ('super-admin','tenant-admin'))`）曾在 DB 层强制角色与租户的关系。自定义角色（ADR-0017 Phase-2）落地后，约束无法枚举 `roles` 表中的角色名，故由迁移 `00013_operators_role_check.sql` **删除**这两个 CHECK 约束。对应的 scope_kind 一致性校验改为应用层在 handler 边界执行：

```sql
-- 迁移 00013 已删除以下约束（保留于此仅作历史记录）：
--   ALTER TABLE operators DROP CONSTRAINT operator_role_tenant;
--   ALTER TABLE operators DROP CONSTRAINT operators_role_check;  -- CHECK (role IN ('super-admin','tenant-admin'))
```

> 迁移 00013 的 Down 脚本会重新加回这两个约束，但仅用于测试回滚，生产环境不期望运行 Down。

### `audit_logs.tenant` 列

00003 迁移新增的 nullable `tenant VARCHAR`（ADR-0019）。**ADR-0014 写于该列添加前，文本未反映**。语义：归属受影响租户（不是操作者租户）。NULL 表示全局/平台级操作（config CRUD、tenant 创建）。让 tenant-admin 能读到针对本租户的操作审计（含 super-admin 对本租户的操作）。

### `audit_logs.operator_id` 无 FK

设计意图：operator 删除后审计行保留（历史完整性 > 引用完整性）。`sessions.operator_id` 是唯一带 `ON DELETE CASCADE` 的 FK（session 是瞬时态，operator 被开除应立即失效所有 session）。

---

## §5 设计决定与一致性说明

集中记录"为什么这样设计"，每条带 ADR/代码引用。

### 5.1 PG 不是 cross-refs 完整性权威

> cross-refs 在 admin service 层写时校验→400，PG 不强制（ADR-0014 §1）。

跨表引用（route→provider、model upstream→provider、route.model_alias→model.alias）都是 **name string 软引用**，靠 admin handler 写入时校验存在性 + 子集约束：

- Route POST：校验 `ModelAlias` 存在 + `Route.Providers ⊆ Model.Upstreams`（`internal/admin/crud.go:163-189`）
- Model POST：反向校验更新后引用它的 Route 仍 ⊆（`internal/admin/crud.go:106-130`）
- Model POST：校验 `Upstreams[].Provider` 存在（`internal/admin/crud.go:101-111`）

删除引用保护：409 拒绝（`ProviderReferencedBy` / `ModelReferencedBy`，`internal/store/config.go:272-302`）。

### 5.2 `plugins` 表无 UNIQUE 约束

其他 3 张 config 表都有 UNIQUE（`name`/`alias`/`model_alias`），`plugins` 没有。原因：

- plugins 的唯一键是 `(name, scope)` 组合，scope 可空（`''` = global）
- UpsertPattern：DELETE+INSERT（`internal/store/config.go:172-185`），非 `ON CONFLICT`
- 应用层唯一性校验，SQL 层不强制

### 5.3 `audit_logs.operator_id` 无 FK

`audit_logs.operator_id` 定义为 `BIGINT`（nullable，无 `REFERENCES`）。设计意图：**operator 删除后审计行保留**（历史完整性优先于引用完整性）。ADR-0017 §5 未明确陈述，本文件补充标注。

对比：`sessions.operator_id` 是唯一带 `ON DELETE CASCADE` 的 FK —— session 是瞬时态，operator 被开除应立即失效所有 session；审计行是永久态，必须保留。

### 5.4 `audit_logs.before` 列 phase-1 未使用

`00001_initial_schema.sql:134` 定义 `before JSONB`，但 `internal/store/audit.go:42-45` 的 INSERT 不写入 `before`。`audit.go:20` 注释承认："phase-1 records After — the request payload; a before-snapshot is a later enhancement"。

phase-1 只记录 `after`，`before` 快照是后续增强。

### 5.5 `provider_credentials` 表（加密落库，已实现）

> 字段表见 §2.3。本节记录设计背景与决策。

原始设计文档（`docs/plans/2026-06-29-llm-gateway-design.md`）曾列出此表，ADR-0014 一度 defer，但随后由 [ADR-0031](../docs/adr/0031-provider-credential-encryption-at-rest.md) 落地实现。迁移 `internal/store/migrations/00009_provider_credentials.sql` 建表。

现状：上游凭证以 AES-256-GCM 加密存于 `provider_credentials`（`provider_name` PK + `ciphertext BYTEA` + `nonce` + `algorithm` + `key_version`，`FK ... REFERENCES providers(name) ON DELETE CASCADE`）。`providers.spec.api_key_ref` 仍存 ADR-0003 引用串，新增 `db://provider/<name>` scheme 指向加密行，与 `env://VAR` / `plain://literal` / 裸字面量并存。凭证经专用 `PATCH /api/v1/providers/{name}/credential` 端点轮换，**无需重启数据面**（dispatcher 重建时解密取用）。明文仅存于进程内存，绝不落库、快照或日志。

### 5.6 `audit_logs.tenant` 列是后加的

00003 迁移新增（`ALTER TABLE audit_logs ADD COLUMN tenant VARCHAR`）。ADR-0014 写于该列添加前，文本未反映。现状：nullable，ADR-0019 §4 解释受影响租户语义。pre-existing 行保持 NULL（全局），是正确默认值。

### 5.7 `quotas.scope` 是扁平字符串（非 FK）

见 §3.3。scope 是 free-form 字符串，按命名约定识别层级。`tenant:` 前缀校验租户存在，`group:` / `key:` 不校验（允许预充值）。

### 5.8 `request_logs` 与 `trace_payloads` 为什么分两张表

ADR-0021 §2 明确禁止 `request_logs` 存 prompt/completion 正文，ADR-0039 §1 据此引入 `trace_payloads` 作为独立的正文账本。两表 1:1 配对但**不通过 SQL JOIN 连接**（应用层按 `request_id` 合并）。拒绝合并（即拒绝给 `request_logs` 加 `messages` / `request_body` / `response_body` JSONB 列）的 5 个理由：

1. **数据性质**：`request_logs` 是业务/合规元数据（token 数、耗时、错误），`trace_payloads` 是高敏感的 prompt/completion 明文（可能含 PII）。
2. **保留期**：`request_logs` 长期保留（排障/审计），`trace_payloads` 默认 7 天（partition-DROP）。合并会让两者相互拖累——正文跟着长留（成本/隐私风险），或元数据跟着短留（丢失排障能力）。
3. **体积**：`request_logs` 每行几百字节（固定列），`trace_payloads` 每行 KB-MB（JSONB + TOAST 压缩）。合并会让 session 列表的 `GROUP BY` 扫过巨大 JSONB 列，性能崩盘。
4. **访问控制**：`request_logs` 读不审计，`trace_payloads` 每次详情读写 `audit_logs` 行（ADR-0039 §5）。合并无法在表级别区分"读敏感列"和"读普通列"。
5. **开关策略**：`request_logs` 永远写（100% 账本），`trace_payloads` 默认关、热重载（隐私优先，显式 opt-in）。合并无法独立 fail-open。

### 5.9 `gateway_settings` 单行表 + JSONB 的热重载设计

> 字段表见 §2.4。本节记录设计背景。

`gateway_settings` 用单行表（`CHECK id = 1`）+ JSONB spec 承载热重载的网关行为参数。设计要点：

- **单行设计**：全局唯一文档，无 per-row 增删，只能整体替换（`PUT /api/v1/gateway-settings`）。同 `config_generation`——是全局状态而非实体集合。
- **JSONB forward-compatible**：加参数只需改 Go struct `config.GatewaySettings`，**不需要 SQL migration**（spec 自动 round-trip）。同 config 表家族（providers/models/routes/plugins）。
- **热重载机制**：每次写入同一事务 bump `config_generation`，数据面通过 snapshot poll 检测到版本变化后重载 `Dynamic.Settings`，~5s 生效无重启。
- **无独立 ADR**：该表作为 ADR-0039 §2 热重载需求的载体表引入（原始的 `trace.capture_payload.enabled` bootstrap YAML 被 `gateway_settings.trace.capture_payload_enabled` 取代），未单独开 ADR。

---

## §6 已知缺口速查

> 注：Group CRUD、Tenant 软删 toggle、Provider 凭证加密落库（ADR-0031）、`request_logs` 读 API
> 均已实现并进入 OpenAPI，不再列为缺口。

| 缺口 | 状态 | 引用 |
|---|---|---|
| `audit_logs.before` phase-1 未使用 | 只写 after | `internal/store/audit.go:20` |
| Tenant 级模型可见性 | Model 是全局资源，与 tenancy 正交 | ADR-0014:87-91 |
| Group 级模型作用域 | ADR-0005 声明但未实现 | ADR-0005:29-30 |

---

## §7 元数据表

### `config_generation`

单行表 `version BIGINT`，初始 0。每次 config 写入（provider/model/route/plugin 的 upsert/delete）在同一事务里 `version = version + 1`（`internal/store/config.go:28-30` `bumpGeneration`）。数据面轮询时读此值作为 snapshot ETag（`If-None-Match` 匹配返回 304）。

### `goose_db_version`

goose 框架自动管理，记录已应用的迁移版本。

### 两者是不同生命周期

> snapshot version (`config_generation`, config *content*) ≠ schema migration version (`goose_db_version`, schema *shape*)。永不相混。（ADR-0015:76-78）

- `config_generation` 跟内容变化（每次 admin 写入 bump）
- `goose_db_version` 跟 schema 形状变化（每次迁移 bump）

加 config 字段不需要 migration（JSONB spec 自动 round-trip），但加表/列/约束需要 migration。

---

## 引用

- ADR-0005：租户层级 Tenant→Group→APIKey
- ADR-0006：API Key 鉴权与数据通道
- ADR-0013：配额数据面直连 PG + 微单位
- ADR-0014：管理面 schema（混合身份列+JSONB、全局/租户划分）
- ADR-0015：迁移与快照版本
- ADR-0016：数据服务设计（多 scope 扣减、异步用量记录）
- ADR-0017：RBAC + scoped-repo 隔离 + 审计
- ADR-0019：读 API + audit_logs.tenant 列
- ADR-0021：`request_logs` 数据面审计账本（明确禁止存正文的约束源头）
- ADR-0024：`data_plane_nodes` 数据面���例注册与心跳
- ADR-0025：`config_snapshots` 配置版本历史
- ADR-0027：PII redaction（未来叠加在 `trace_payloads` 正文上，无需 schema 变更）
- ADR-0029：月度分区 + partition-DROP TTL（`request_logs` / `usage_records` / `trace_payloads` 共用）
- ADR-0031：`provider_credentials` 加密落库
- ADR-0032：OpenAI 透传保真（`UnifiedResponse.Raw` 是 `trace_payloads.response_raw` 的来源）
- ADR-0039：`trace_payloads` 4 层 trace 模型（Session → Request → Messages → Raw）
- `design/domain-flows.md`：实体生命周期、引用保护、quota scope 命名约定
- `design/observability.md`：语义字段（model/provider/token/ttft/cache/拦截/upstream_request_id）
- `internal/store/migrations/00001-00024_*.sql`：schema 真相源（缺 00018）
- `internal/config/schema.go`：spec JSONB 的 Go 结构定义
- `internal/store/config.go`：ConfigRepo CRUD + 删除引用保护
- `internal/store/tenant.go`：TenantRepo + Group 结构
