# voxeltoad Roadmap

> 本文档是项目演进的**单一事实来源**，明确各主线阶段及触发条件。
> 更新日期：2026-07-31

---

## 项目定位

**双主线并行**——Enterprise Evolution 与 Desktop Productization 并列推进，共享
`internal/proxy` / `internal/adapter` / `internal/auth` / `internal/observability` /
`internal/config` 核心。Desktop 继续承担编译期 canary，任何改核心的 PR 都会被
desktop 编译失败先撞到。

企业演进设计快照见 [docs/plans/2026-07-31-enterprise-evolution.md](plans/2026-07-31-enterprise-evolution.md)，
核心决策见 [ADR-0051](adr/0051-application-governance-identity.md)～[0056](adr/0056-harness-run-and-tool-audit-boundary.md)。

---

## P0 已完成

以下能力已生产可用，非骨架：

- [x] **协议适配**：OpenAI / Claude 完整 adapter（含 SSE 流式），tencent/zhipu 通过 openai adapter 走配置分支
- [x] **配额计费**：Pre 预扣 + Post 结算，失败退还；token 来自上游 response.usage（非估算）
- [x] **限流**：单机内存 sliding-window，多实例靠"总额除以在线节点数"妥协
- [x] **审计**：管理面 `rbac.auditMutation` 中间件统一拦截非 GET 写操作；数据面每请求落 `request_logs`
- [x] **多租户**：middleware 层强制，handler 不重复判
- [x] **provider_credentials 加密**：AES-256-GCM 已落地（ADR-0031）
- [x] **OpenAPI 契约**：43 个端点，server 端实现度高；SDK codegen 用 `git diff --exit-code` 强制同步
- [x] **数据库**：23 个迁移文件、24 张表、月度分区；database.md 与 migrations 同步纪律好
- [x] **前端控制台**：Next.js 16 + React 19 + RSC，20 个 dashboard 页面全部「真实可用」档
- [x] **SDK**：`@voxeltoad/gateway-sdk` 双产物（数据面 client + 管理面 admin），web 强依赖
- [x] **测试**：~101 个 _test.go、test/e2e/ 16 个文件、`make ci` 含 16 个 step
- [x] **CI**：GitHub Actions 双 job（ci-light / ci-heavy）

---

## 当前主线一：Enterprise Evolution

> 设计决策已 Accepted（ADR-0051～0056），实现按 E0～E3 分期推进。
> 每个切片必须同步 migration、`design/database.md`、`docs/openapi/admin.yaml`、SDK、权限/错误码/i18n 和测试。

### E0 可见（Application 归因）

**目标**：让每条请求都能回答"哪个应用、什么环境在消费"。

- [ ] Application CRUD + 生命周期（enabled/disabled）
- [ ] APIKey 强绑定 Application + environment 受控属性
- [ ] `usage_records` / `request_logs` / `trace_payloads` 新增 `application_id` + `environment` 快照
- [ ] 控制台 Application 管理页 + usage/request 按应用维度查询
- [ ] 历史孤儿 Key 扫描与未归因流量量化

**验收**：新生产 Key 绑定率 100%；未归因历史流量可量化；Application 停用在 key cache TTL 内阻断所有绑定 Key。

**非目标**：预算、智能路由、Agent 一等化。

**前置**：`api_keys.group_id` nullable 漂移处理——先扫描/归属历史孤儿 Key，再决定 `NOT NULL`。

### E1 管控（预算与 kill switch）

**目标**：应用/环境级别的成本与容量控制。

- [ ] BudgetPolicy / BudgetAccount migration + 周期 rollover
- [ ] Cost Hard Budget：Tenant / consuming Group / Application / env / Key 交集原子预留
- [ ] Token Allowance：post-accounting（完成后记账、耗尽后拒绝后续）
- [ ] Soft Budget：阈值告警事件
- [ ] 预算驱动预设降级 + 降级后仍不足则拒绝
- [ ] Kill switch：Tenant / Application / APIKey 同步停用

**验收**：并发原子性无越界；结算幂等；rollover 与晚到结算归属正确；停用在 key cache TTL 内生效。

**非目标**：queueing、approval、borrowing、mid-stream truncation。

### E2 优化（可解释路由）

**目标**：从静态 provider 路由演进到可解释的成本—质量—延迟决策。

- [ ] RoutingDecision 不可变账本（候选集、淘汰原因、策略版本、信号快照、attempt、结果）
- [ ] Application RoutingPolicy：硬约束过滤 → Quality Tier → ordered objective → 预算降级 → 预留 → health/failover
- [ ] 私域滚动遥测（TTFT、吞吐、错误率）+ 最小样本/陈旧回退
- [ ] 控制台路由决策查询与回放

**验收**：决策记录覆盖率 ≥ 99.9%；规则回放结果 100% 确定；低样本/陈旧信号稳定回退；fail-open dropped 可观测。

**非目标**：语义路由、学习型路由、外部市场数据自动改路由。

### E3 资产化（反馈与数据集）

**目标**：让网关数据形成可治理、可评测、可反馈的闭环。

- [ ] FeedbackEvent 账本 + 鉴权 + 血缘 + 导出
- [ ] Dataset / DatasetVersion / DatasetItem 元数据 + 血缘
- [ ] Payload promotion（授权 + 脱敏 + 审计）
- [ ] ContentStore：PG 默认 / object 可配置；未配置不影响核心网关
- [ ] 评测数据导出

**验收**：晋升授权/脱敏/审计覆盖率 100%；payload backend 故障不影响调用主链；FeedbackEvent 不复制正文。

**非目标**：内置评测平台、训练平台、judge 执行。

---

## 当前主线二：Desktop Productization

### desktop 个人网关（ADR-0041）

**目标用户**：个人开发者（作者本人即用户），有多个 LLM 调用源（CodeBuddy/Codex/Claude Code/脚本），需要本地 `127.0.0.1` 收敛入口 + **被动录制所有 prompt/completion 用于学提示词**。

**明确排除**：多租户、RBAC、配额、跨实例一致性。

**当前状态**：
- [x] SQLite store + 配置 + 主入口 + UI 骨架
- [x] provider/model/route CRUD + 热重载
- [x] Wails v2 工程 + macOS .app target
- [ ] **desktop .dmg 发布准备**——面向个人开发者发布安装包 + 使用文档

**复用关系**：核心零改动复用；差异收敛在 `internal/desktopstore`（SQLite 替代 PG）、`internal/desktopapi`（无 RBAC 读 API）、`cmd/desktop`（组合根）。

**编译期 canary**：任何改 `internal/proxy` 的 PR 都会被 desktop 编译失败先撞到。

### UI 产品级化（2026-07 启动）

短期目标：控制台与 desktop-ui 从「功能真实可用」提升到「体验产品级」。规范与门禁先行，
缺口分批收敛，单一事实来源是 [design/design-system.md](../design/design-system.md)。

- [x] **P0（2026-07-18 完成）**：设计规范升级 + `make check-ui` 门禁上线；Modal 规范化；usage 图表色值修复；品牌 logo 落地；emoji/字符画图标清零；`dark:` 死类清零；非白名单彩虹色 token 化
- [ ] **P1 一致性批次**：trace 彩虹色收敛、Badge/EmptyState 推广、表格 4 变体收敛、详情页模板推广、原生 `<select>` 残余迁移、FilterField 抽取、i18n 硬编码文案、语言切换 UI、loading.tsx 按需铺设
- [ ] **P2 韧性批次**：全局 error.tsx/not-found.tsx、usage 静默吞错修复、overview 弱类型、spacing/radius/typography scale token 化

**约束**：`make check-ui` 已挂入 CI，白名单只减不增；新增 UI 基元/token/模板必须同步 design-system.md。

---

## 等触发

以下方向**设计已完备，等待触发条件**：

### 多实例方向（ADR-0034~0038）

| ADR | 内容 | 状态 | 触发条件 |
|---|---|---|---|
| 0034 | Redis 共享状态（RedisLimiter / RedisCache / RedisCircuitBreaker） | Proposed | 第一位要求 `replicaCount > 1` 的客户出现 |
| 0035 | PG 连接池（`db.pool` 配置块 + 4 个旋钮） | Proposed | 单实例 QPS 超阈值 |
| 0036 | 动态限流除法（心跳驱动每 15s 重算） | Proposed | 多实例上线 |
| 0037 | 集群部署拓扑（描述性文档） | Proposed | 多实例上线 |
| 0038 | 节点生命周期（diagnostic only） | Diagnostic | 无需实施 |

**当前妥协**：限流"总额除以在线节点数"（`cmd/gateway/main.go:202-212`），Helm 默认 `replicaCount: 1`。

### WASM 插件（ADR-0022）

**状态**：Proposed，ABI v1 契约已完备。

**触发条件**：高级用户/企业客户真实需求。

### 5 个插件挂主链

| 插件 | 代码完整度 | 状态 | 触发条件 |
|---|---|---|---|
| cache | **不完整**（只实现 Cache 接口） | 孤儿代码 | 真需要响应缓存时 |
| sensitive | 完整 | 已 Register，未挂链 | 企业客户合规审查需求 |
| pii | 完整 | 已 Register，未挂链 | 企业客户合规审查需求 |
| injection | 完整 | 已 Register，未挂链 | 企业客户安全需求 |
| moderation | 完整 | 已 Register，未挂链 | 企业客户内容审核需求 |

### Helm chart 完整化

**当前状态**：`deploy/helm/` 真实可部署但功能有限。

**触发条件**：多实例上线。

### 企业演进触发式方向

| 触发条件 | 启动方向 | 关联 ADR |
|---|---|---|
| 接入真实 vLLM/llm-d 推理集群 | GPU/KV/队列 accounting + 资源调度 | 0052 |
| 可信 Agent delegation 企业需求 | Agent Principal + 委托链 + 工具权限 | 0051, 0056 |
| MCP/A2A 企业工具治理需求 | 协议代理 + 工具发现 + OAuth + 审批 | 0056 |
| 足够反馈覆盖率 | semantic routing → learned routing | 0053 |
| 自动化评测成熟 | 自动晋升规则 + 训练数据导出 | 0055 |

---

## 触发条件汇总

| 触发条件 | 影响方向 |
|---|---|
| 第一位要求 `replicaCount > 1` 的客户出现 | ADR-0034/0035/0036/0037（多实例） |
| 单实例 QPS 超阈值 | ADR-0035（PG 连接池） |
| 高级用户/企业客户真实需求 | ADR-0022（WASM 插件） |
| 企业客户合规审查需求 | sensitive/pii 插件挂链 |
| 企业客户安全需求 | injection 插件挂链 |
| 企业客户内容审核需求 | moderation 插件挂链 |
| 真需要响应缓存时 | cache 插件补完 + 挂链 |
| 接入真实自建推理集群 | GPU accounting（ADR-0052 self_hosted_compute） |
| 可信 Agent delegation 需求 | Agent Principal + 委托链（ADR-0051/0056） |
| MCP/A2A 企业工具治理需求 | 工具代理 + 发现 + 执行（ADR-0056） |
| 足够反馈覆盖率 | semantic/learned routing（ADR-0053） |

---

## 更新记录

- 2026-07-17：初版，基于 grill session 拍板结果
- 2026-07-18：新增「UI 产品级化」批次（P0 完成，P1/P2 排期）；design-system.md 升级为视觉单一事实源 + `make check-ui` 门禁
- 2026-07-31：重构为双主线（Enterprise Evolution + Desktop Productization）；新增 Enterprise E0～E3 分期与触发式方向；企业演进决策 ADR-0051～0056 Accepted
