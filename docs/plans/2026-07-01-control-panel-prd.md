# PRD：voxeltoad 管理面（Control Panel）—— 后端 API

- 状态：需求分析 + 设计（2026-07-01）；本阶段只做**后端 API**，不含前端 UI
- 范围：管理面（`cmd/admin` 运行的 REST API）。数据面见 [data-plane PRD](2026-07-01-data-plane-prd.md)
- 关联设计：ADR-0014（schema）、ADR-0017（RBAC）、**ADR-0019（读取面 + 配额充值一致性）**
- 一句话定位：把数据面已持久化的运营数据（用量/审计/配额）变得**可读可运营**，并补齐配置治理的管理动作（groups/operators/配额充值），全部遵循 ADR-0017 的操作员鉴权与租户隔离。

---

## 1. 背景与目标

### 1.1 现状（Step 7.5/7.6 已建）
管理面**写**面已就绪：操作员登录 + 会话、super-admin/tenant-admin 两角色 RBAC、全局配置 CRUD（providers/models/routes/plugins）、租户与 api-keys 管理、配置快照 endpoint、bootstrap。所有 mutation 已审计。

### 1.2 问题
数据面持续写入 `usage_records`（每请求）、`audit_logs`（每 mutation）、`quotas`（每次扣费/结算），但这些数据**没有任何读取 API**；配额只能内部 `SetBalance`（绝对覆盖，会与热路径竞争）；operators/groups 管理缺失。运营方无法回答"这个租户这个月花了多少 / 谁改了配置 / 给某租户充值"。

### 1.3 目标（本阶段）
- **观测读取面**：用量查询与聚合、审计检索、配额余额查看。
- **配额运营**：安全的充值/调整（原子增量，不与扣费竞争）。
- **配置治理补全**：groups CRUD、operators 管理、tenant 删除。
- 全部遵循 ADR-0017 鉴权/隔离；读取不审计、配额变更审计（ADR-0019）。

### 1.4 非目标（phase-2）
前端 UI；物化用量汇总表；读操作审计；成本账单/发票生成；跨租户 BI 仪表盘；多实例共享限流/熔断态（数据面 phase-2）。

---

## 2. 角色与可见性（沿用 ADR-0017）

| 角色 | 全局配置 | 租户资源(自租户) | 观测读取 | 配额运营 | 操作员管理 |
|---|---|---|---|---|---|
| **super-admin** | ✅ CRUD | ✅ 任意租户 | ✅ 全局（任意租户 + 全局配置审计） | ✅ 任意 scope 充值 | ✅ 管理所有操作员 |
| **tenant-admin** | ❌ | ✅ 仅自租户 | ✅ 仅自租户（用量/审计/配额余额，含 super-admin 对本租户的操作审计） | 只读自租户配额余额 | ❌ |

租户隔离是**结构性**的：tenant-admin 的读取经"构造时绑定 tenantID"的 query repo，无法表达跨租户查询（ADR-0017 §3 / ADR-0019）。

---

## 3. 功能需求

### 3.1 观测读取面（首要，ADR-0019）

| # | 能力 | 端点（建议） | 角色 | 说明 |
|---|---|---|---|---|
| U1 | 用量明细查询 | `GET /api/v1/usage?from&to&cursor&limit[&model&provider&api_key_id]` | 两者（tenant-admin 限自租户） | 时间范围必填；keyset 分页（`(created_at,id)`）；命中 `(tenant,created_at)` 索引 |
| U2 | 用量聚合 | `GET /api/v1/usage/summary?from&to&group_by=day\|model\|provider\|key` | 两者 | SQL `GROUP BY` 聚合 tokens/cost；read-time 计算（无物化） |
| A1 | 审计检索 | `GET /api/v1/audit?from&to&cursor&limit[&resource_type&action&operator_id]` | super-admin（全局 + 全局配置审计）/ tenant-admin（仅本租户,按 `audit_logs.tenant` 过滤,含 super-admin 对本租户的操作） | newest-first；keyset 分页；新增 `audit_logs.tenant` 列 + `(created_at)`、`(tenant,created_at)` 索引 |
| Q1 | 配额余额查看 | `GET /api/v1/quotas?scope=` 或 `GET /api/v1/tenants/:t/quotas` | 两者（tenant-admin 限自租户 scope） | PK 查找，廉价 |

**关键约束（ADR-0019 §2）**：所有列表端点**必须时间范围有界 + keyset 分页 + limit 上限**;usage_records 高写入,禁用 OFFSET 深翻页。审计 tenant-admin 可见范围待定(audit_logs 目前只有 operator/resource,无 tenant 列)——见 §7 待决项。

### 3.2 配额运营（ADR-0019 §3）

| # | 能力 | 端点 | 角色 | 语义 |
|---|---|---|---|---|
| Q2 | 配额充值/调整 | `POST /api/v1/quotas/topup` `{scope, delta, currency}` | super-admin | **原子增量** `balance += delta`（正=充值/负=扣减修正），单条 SQL，永不与热路径 `TryDebit`/`Settle` 竞争。**审计**（resource_type=quota, resource_id=scope, after=delta） |
| Q3 | 新 scope 初始额度 | 复用 `SetBalance`（绝对） | super-admin | 仅用于全新 scope 首次开额；已存在 scope 一律用 topup |

**为什么不能直设绝对值**：`SetBalance` 是 `ON CONFLICT DO UPDATE SET balance = EXCLUDED.balance`（覆盖）;管理面读后写会吞掉两次读写之间的并发扣费。原子增量与 ADR-0013 "钱的变更是单条原子 SQL" 一致。

### 3.3 配置治理补全（机械 CRUD，沿用 ADR-0017 模式）

| # | 能力 | 端点 | 角色 | 备注 |
|---|---|---|---|---|
| G1 | groups CRUD | `POST/GET/DELETE /api/v1/groups` | tenant-admin | `TenantRepo` 已有 `CreateGroup/ListGroups`，补 DELETE + HTTP 暴露 |
| O1 | 操作员列表/创建/停用 | `GET/POST/DELETE /api/v1/operators` | super-admin | 创建 tenant-admin（绑定租户）、停用（删除→级联撤销 sessions）；`OperatorRepo` 需补 List/Delete |
| O2 | 会话查看/吊销 | `GET/DELETE /api/v1/operators/:id/sessions` | super-admin | `SessionRepo` 已有 `DeleteByOperator`；补 List |
| T1 | 租户删除/停用 | `DELETE /api/v1/tenants/:name` | super-admin | 现只有 create/list；删除需考虑级联（groups/keys/quota）——见 §7 |

### 3.4 已完成（不在本次范围，仅记录）
providers/models/routes/plugins CRUD、tenants create/list、api-keys CRUD、login、snapshot、bootstrap、审计中间件。

---

## 4. 用户故事（节选）

- **运营/财务**：作为 super-admin,我要按租户+月查询 token 用量与成本(U1/U2),以便对账与成本分摊。
- **租户管理员**：作为 tenant-admin,我要看本租户本月各模型的调用量与花费(U1/U2 限自租户),但**看不到别的租户**。
- **合规/安全**：作为 super-admin,我要检索"谁在何时改了哪个 provider/key"(A1),以便审计追溯。
- **运营**：作为 super-admin,我要给某租户充值 100 美元额度(Q2),且**不会因为该租户此刻正在高频调用而丢失充值或吞掉扣费**。
- **管理员**：作为 super-admin,我要新建一个 tenant-admin 操作员并绑定到某租户(O1),离职时能停用并立即吊销其所有会话。

---

## 5. 技术设计要点

### 5.1 读取仓储（tenant-bound）
新增 `UsageQueryRepo(db, tenantID)`、`AuditQueryRepo(db, tenantID)`:构造时绑定租户,每个查询硬编码租户过滤;super-admin 用非绑定变体或 `tenantID==0 ⇒ 全局` 约定(保持过滤结构性,不可遗忘)。查询签名带 `[from,to)` + keyset cursor + limit。

### 5.2 分页与性能
- usage:keyset on `(created_at, id)` DESC;命中 `idx_usage_records_tenant_created` / `idx_usage_records_created_at`;月分区裁剪。
- audit:新增迁移 `CREATE INDEX ON audit_logs (created_at)`;查询恒带时间边界。
- 聚合:`GROUP BY` on 时间有界 + 租户过滤的扫描;read-time,无物化(phase-2 再优化)。

### 5.3 配额充值一致性
`QuotaRepo.TopUp(ctx, scope, delta, currency)` = 单条 `INSERT…ON CONFLICT DO UPDATE SET balance = quotas.balance + EXCLUDED.balance`。绝不 app 内 read-modify-write。并发测试:TopUp 与 TryDebit/Settle 交错,最终余额 = 初始 + 充值 − 扣费(无丢失更新)。

### 5.4 复用现有 seam
authnMiddleware → requireSuperAdmin/requireTenantAdmin → `operatorFrom(c)` → `store.NewXxxRepo(db, *op.TenantID)`;mutation 经 `auditMutation` 中间件写审计;读端点不挂 auditMutation。

---

## 6. 优先级与实施顺序（建议）

| 优先级 | 项 | 理由 |
|---|---|---|
| **P0** | Q2 配额充值(原子增量) + 并发测试 | 有真实正确性风险(丢失更新);运营刚需 |
| **P0** | U1/U2 用量查询+聚合 | 首要观测诉求;对账/成本分摊 |
| **P1** | A1 审计检索(+ audit 索引迁移) | 合规追溯;数据已在 |
| **P1** | Q1 配额余额查看 | 廉价;配合 Q2 |
| **P2** | G1 groups CRUD、O1/O2 operators 管理、T1 tenant 删除 | 机械补齐;沿用现有模式 |

每步 test-first:store 层 dbtest(隔离/分页/聚合/并发) + admin httptest + e2e(harness 上跨租户隔离与充值一致性)。

---

## 7. 已决策项（原待决，现拍板）

1. **audit_logs 租户可见性 → tenant-admin 可看本租户审计**。给 `audit_logs` 加可空 `tenant` 列;审计中间件按**受影响租户**填充(不是操作者租户)——super-admin 对某租户的操作(充值/改 key/建 tenant-admin)该租户能看到;全局配置操作 tenant=NULL(仅 super-admin 可见)。历史行 NULL。加索引 `(created_at)`、`(tenant, created_at)`。
2. **tenant 删除 → 软删**(`enabled=false`),保留 usage/audit 引用完整,与 api_keys 的 `revoked_at` 一致。其他资源删除同理优先软删。
3. **审计归属按受影响租户**:middleware 加 per-resource-type 的 tenant 解析器(scope `tenant:X`→X、api-key→查所属、全局→NULL)。
4. **契约与 SDK → OpenAPI-first**:admin API 写 OpenAPI 3 spec 作单一事实源,codegen TS admin 客户端供 UI + 契约测试共用;Go 端按 spec 校验请求/响应。数据面 OpenAI 兼容 SDK 保持不变,admin 是独立生成客户端。
5. **列表统一信封 + CORS**:所有 list 端点返回 `{data, next_cursor}`(含现有 config lists,裸数组迁移到信封);admin 加可配 CORS 中间件(允许 origin 从配置来,空=同源)。
6. **聚合货币**:cost 按 currency 分组返回,不做汇率换算。
7. **tenant-admin 只读自租户配额余额**:放开(结构性隔离已支持,零成本)。

---

## 8. 契约、SDK 与可测性（前后端分离就绪）

目标：API **易测**、**便于后续 UI 开发**、**支持前后端分离**、**可通过 SDK 测试**。方案:OpenAPI-first(§7.4)。

### 8.1 契约单一事实源
- 为 admin API 维护 **OpenAPI 3 spec**(checked-in)。所有请求/响应/错误信封/分页 cursor 结构在 spec 里定义。
- Go handler 在测试中按 spec 校验(请求绑定 + 响应结构),保证 spec 权威而非"墙上文档"。

### 8.2 SDK / 生成客户端
- 从 spec **codegen TS admin 客户端**(如 `openapi-typescript` + fetch,或 openapi-generator)。UI 与测试**共用生成客户端**,类型不漂移。
- 数据面 OpenAI 兼容 SDK(`VoxeltoadGateway extends OpenAI`)**保持不变**;admin 是独立生成的客户端/命名空间(如 `@voxeltoad/admin-client` 或 `gw.admin.*`),二者职责分离。
- **可通过 SDK 测试**:契约测试用生成客户端打真实 admin(跑在 e2e harness 上),登录 → 建租户 → 充值 → 查用量/审计,断言类型与状态码。这也成为未来回归与 UI 联调的基线。

### 8.3 统一响应契约(UI 友好)
- **列表信封** `{data: [...], next_cursor: ""}` 全端点统一(含现有 config lists);`next_cursor` 空=末页。单一 client 解析形态。
- **错误信封** 沿用现有 `{error:{message,type}}`(与数据面一致)。
- **时间戳** ISO-8601 UTC;**money** int64 micro-units + currency 字段(客户端负责展示换算)。

### 8.4 前后端分离
- **CORS** 中间件(可配 allowed origins,空=同源安全默认),支持 UI 独立 origin 直连 admin API。
- 无服务端渲染耦合:admin 纯 JSON REST,UI 任意技术栈独立部署。
- 会话为 Bearer token(现有),UI 存 token 走 `Authorization` 头;CORS 放行该头。

### 8.5 可测性分层
- **store 层**:dbtest(embedded-pg)—— 隔离、keyset 分页、聚合 SQL、**TopUp×TryDebit 并发无丢失更新**。
- **admin HTTP 层**:httptest —— 角色鉴权、信封结构、审计写入。
- **e2e**:harness 上生成客户端契约测试 —— 全链路 + 跨租户隔离 + super-admin 对租户操作可被该租户审计看到。

---

## 9. 结论

管理面的**写面已完成**;本 PRD 聚焦把已持久化的运营数据变为**可读可运营**,补齐配置治理动作,并以 **OpenAPI-first + 统一信封 + CORS** 为前后端分离/UI 开发/SDK 测试铺路。核心设计决策(读取面分页/隔离/性能、配额充值并发一致性、审计受影响租户归属、契约与信封)已在 ADR-0019 + §7 定稿,原待决项全部拍板。实施按 §6 优先级、test-first 推进;P0(配额原子充值 + 用量查询)不依赖迁移,可先行。
