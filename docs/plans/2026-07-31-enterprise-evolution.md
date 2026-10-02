# 企业级特性演进设计快照

> 日期：2026-07-31
> 状态：设计决策已 Accepted（ADR-0051～0056），实现按 roadmap E0～E3 分期推进
> 关联：`docs/roadmap.md`（演进主线）、`docs/adr/0051`～`0056`、`docs/glossary.md`

---

## 1. 背景与动机

voxeltoad 已具备完整的基础网关能力：协议适配、配额计费、限流、审计、多租户、
provider credential 加密、OpenAPI 契约、前端控制台和测试体系。但它目前只能回答
"哪个租户/团队/Key 花了钱"，不能稳定回答"哪个应用/Agent 产品/生产任务花了钱、
产出了什么价值"。

企业级网关作为 AI 时代的基础设施，应提供多维度的企业级统计分析和管控：

- **应用维度**的 Token 统计、Token 控制和额度控制，与"发 Key"并存
- **算力成本**的清晰度量与管控
- **网关数据**作为企业数据资产，支撑更好的路由和反馈闭环
- **企业级 Harness** 场景中网关应发挥的治理与度量价值

OpenRouter 的趋势判断印证了：稀缺性正从"模型访问"迁移到**选择、计量、授权、结算**。
企业内部网关数据量不及 OpenRouter，但拥有更丰富的应用上下文、组织边界和结果反馈，
因此更适合做**企业私域路由智能**。

---

## 2. 当前能力基线

| 能力 | 状态 | 关键约束 |
|---|---|---|
| 身份与租户 | 已实现 | Tenant → Group → APIKey；无 Application/Agent 实体 |
| 配额与计费 | 已实现 | PG 强一致 pre-debit/settle 成本余额；scope tenant/group/key |
| 限流 | 已实现 | 单机内存 RPM/TPM 滑动窗口 |
| 路由 | 已实现 | priority/weighted/round_robin/session_affinity + 熔断/failover |
| 请求日志 | 已实现 | `request_logs` 全量元数据，不存正文 |
| Trace 正文 | 已实现 | `trace_payloads` 默认关闭、PG、7 天分区 DROP |
| Agent 检测 | 已实现 | `agent_type` UA 推断标签，仅观测 |
| 控制台 | 已实现 | Next.js 20 个 dashboard 页面 |
| 插件 | 框架已实现 | 生产链仅 rate-limit + billing |

## 3. 关键缺口

- 无 Application / Agent / Run 领域概念
- 无应用/环境维度的预算、Token Allowance 和归因
- 无 RoutingDecision 账本（候选集、淘汰原因、策略版本不可回放）
- 无质量反馈闭环（FeedbackEvent）
- 无受治理的评测数据集（Dataset）
- 无工具调用审计契约（ToolCallAudit）
- 无自建推理算力度量

---

## 4. 目标领域关系

```text
所有权轴
Tenant
├── Group                 谁负责、谁承担成本
└── Application           什么长期业务系统在消费 AI
    └── owner_group_id

凭证轴
Application ── 1:N ── APIKey
APIKey 仍归属消费方 Group；Key 轮换不改变应用身份
APIKey.environment        dev/staging/prod 受控属性

运行轴
Application
└── Session (client-supplied, untrusted)
    └── Invocation        一次模型调用

决策轴
Invocation
├── RoutingPolicy         Application 拥有的版本化策略
├── RoutingDecision       不可变决策账本
├── UsageEvent            实际消耗与价格快照
├── FeedbackEvent         质量与业务结果（append-only）
└── DatasetItem           受控晋升的评测数据
```

### 概念边界

| 概念 | 稳定性 | 是否身份 | 主要用途 |
|---|---|---:|---|
| Application | 长期 | 是 | 预算、模型权限、路由策略、成本归属 |
| Agent Label | 中长期配置 | 否 | 统计、软策略；非授权主体 |
| Session/Run | 临时 | 不可信 | 统计投影、软告警；非安全边界 |
| WorkloadProfile | 每请求动态 | 否 | 延迟、质量、成本等路由输入 |

---

## 5. 请求生命周期

```text
客户端请求
  │
  ▼
认证（APIKey → Application + environment 解析）
  │
  ▼
预算与策略评估
  ├─ Hard Budget 交集校验（Tenant / Group / Application / env / Key）
  ├─ Token Allowance post-accounting 检查
  ├─ RoutingPolicy 解析（硬约束 → Quality Tier → ordered objective）
  └─ 预算驱动降级重评估（若需）
  │
  ▼
原子预留（所有适用 Hard Budget 一次事务）
  │
  ▼
路由执行
  ├─ 候选过滤与排序
  ├─ Health/failover（ADR-0011 不变）
  └─ 转发 + 适配
  │
  ▼
结算
  ├─ 按实际 provider 定价 settle
  ├─ Token Allowance post-debit
  └─ 退款（若全失败）
  │
  ▼
异步账本写入（fail-open，不阻塞调用）
  ├─ usage_records
  ├─ request_logs
  ├─ RoutingDecision
  └─ ToolCallAudit（若可见）
  │
  ▼
异步反馈（外部触发，非请求路径）
  └─ FeedbackEvent → Dataset 晋升（管理面操作）
```

---

## 6. 网关在 Agent Harness 中的位置

参考 Agent Harness 的 ETCLOVG 七层模型，网关主要承担：

| 层 | 网关角色 | 说明 |
|---|---|---|
| **O — Observability** | 核心 | Usage、Request、RoutingDecision、ToolCallAudit、FeedbackEvent |
| **G — Governance** | 核心 | 身份、预算、模型权限、kill switch、审计 |
| **L — Lifecycle** | 部分 | 预算周期、停用、降级；不含 Agent loop/记忆/编排 |
| E/T/C/V | 不承担 | 环境、工具执行、上下文管理、验证器由 Harness 平台负责 |

网关是 Harness 的**统一治理与度量平面**，不是 Harness 本身。

---

## 7. 算力与成本边界

| 来源 | 可靠指标 | 实现阶段 |
|---|---|---|
| 供应商 API | Token、价格、延迟、缓存命中 | 已实现 |
| 供应商 API | 成本（微单位） | 已实现 |
| 自建推理 | GPU-seconds、KV-cache、队列时间、批处理效率 | 触发式：接入 vLLM/llm-d 后 |

**禁止**用 Token 数虚构 GPU 成本。自建推理成本需明确的内部计价规则，且与供应商
账单区分。`ResourceUsage` 信封区分 `provider_billed` 与 `self_hosted_compute`。

---

## 8. 明确非目标（首期）

- 不做语义路由 / 学习型路由（需评测集和隐私决策先行）
- 不做 Agent 一等授权主体（需委托链和具体 Harness 需求）
- 不做可信 Run 身份（需签名令牌协议）
- 不做 MCP/A2A 代理、发现或执行
- 不做内置评测平台（judge/排行榜/训练）
- 不做自建 GPU accounting（需真实推理集群接入）
- 不做 queueing/approval/borrowing/mid-stream-truncation（需 Harness lifecycle）

---

## 9. 触发式未来方向

| 触发条件 | 启动方向 |
|---|---|
| 接入真实 vLLM/llm-d 推理集群 | GPU/KV/队列 accounting + 资源调度 |
| 可信 Agent delegation 企业需求 | Agent Principal + 委托链 + 工具权限 |
| MCP/A2A 企业工具治理需求 | 协议代理 + 工具发现 + OAuth + 审批 |
| 足够反馈覆盖率 | semantic routing → learned routing |
| 自动化评测成熟 | 自动晋升规则 + 训练数据导出 |

---

## 10. ADR 索引

| ADR | 主题 |
|---|---|
| [0051](../adr/0051-application-governance-identity.md) | Application 一等治理身份 |
| [0052](../adr/0052-enterprise-budgets-and-resource-accounting.md) | 企业预算与资源核算 |
| [0053](../adr/0053-explainable-application-routing.md) | 可解释应用路由与决策账本 |
| [0054](../adr/0054-feedback-event-and-evaluation-boundary.md) | FeedbackEvent 与评测边界 |
| [0055](../adr/0055-dataset-lineage-promotion-and-storage.md) | Dataset 血缘、正文晋升与存储 |
| [0056](../adr/0056-harness-run-and-tool-audit-boundary.md) | Harness、Run 与工具审计边界 |

---

## 11. 后续代码实现切片

> 仅作为 roadmap 引用，不在本轮文档阶段执行。

1. **E0 可见**：Application schema/repo/API/RBAC → Key binding/environment → usage/request/trace 维度与控制台
2. **E1 管控**：BudgetPolicy/Account → period rollover → 多维原子 reserve/settle → kill switch 与预设降级
3. **E2 优化**：RoutingDecision sink → deterministic RoutingPolicy → private rolling telemetry
4. **E3 资产化**：FeedbackEvent → Dataset metadata/version → PG/object payload driver → promotion/export
5. **触发式**：ToolCallAudit/Run Summary；可信 Run identity、MCP/A2A、自建 GPU accounting

每个切片必须同步 migration、`design/database.md`、`docs/openapi/admin.yaml`、SDK、
权限/错误码/i18n 和测试；统一运行 `make check-docs` 和 `make ci`。
