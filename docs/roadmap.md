# voxeltoad Roadmap

> 本文档是项目演进的**单一事实来源**，明确各主线阶段及触发条件。
> 更新日期：2026-09-30

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
- [x] **配额计费基线**：Pre 预扣 + Post 按上游 response.usage 结算；本次 E1 收窄退款条件，已外呼但费用未知不自动退还（见下文实施状态）
- [x] **限流**：单机内存 sliding-window，多实例靠"总额除以在线节点数"妥协
- [x] **审计**：管理面 `rbac.auditMutation` 中间件统一拦截非 GET 写操作；数据面每请求落 `request_logs`
- [x] **多租户**：middleware 层强制，handler 不重复判
- [x] **provider_credentials 加密**：AES-256-GCM 已落地（ADR-0031）

- [x] **OpenAPI 契约**：43 个端点，server 端实现度高；SDK codegen 重新生成到临时文件并逐字节 diff 校验同步（不要求工作区已提交）
- [x] **数据库基线**：PG 持久化与月度分区；当前迁移/表/字段计数只见 `design/database.md`，不沿用旧里程碑 snapshot 的统计
- [x] **前端控制台**：Next.js 16 + React 19 + RSC，20 个 dashboard 页面全部「真实可用」档
- [x] **SDK**：`@voxeltoad/gateway-sdk` 双产物（数据面 client + 管理面 admin），web 强依赖
- [x] **测试**：145 个 `_test.go`、`test/e2e/` 20 个文件；`make ci` 覆盖 Go、契约与 stack tests，前端门禁在 `ci-web` / `ci-desktop-ui`（CI light job 每 PR 运行）
- [x] **CI**：GitHub Actions 三 job（ci-light / ci-heavy / desktop-windows-build）

---

## 当前主线一：Enterprise Evolution

> 设计决策已 Accepted（ADR-0051～0056），实现按 E0～E3 分期推进。
> 每个切片必须同步 migration、`design/database.md`、`docs/openapi/admin.yaml`、SDK、权限/错误码/i18n 和测试。

### E0 可见（Application 归因）

**目标**：新企业请求使用可信应用/环境身份，历史未归因可查询、不猜测回填。**实现完成，待最终验收**；实现与验收分列，不把测试文件存在当作运行通过。

| 切片 | 当前实现 | 验收 |
|---|---|---|
| Application 与 Key | Application 创建/列表/启停/受引用保护删除；新 Key 强绑定同租户消费 Group + Application + dev/staging/prod；读写权限分离；Application PATCH 返回对象 | 待最终验收 |
| 历史补齐 | `unbound=true` 分页查询，PATCH 一次补齐，不改已有非空身份；变更归属通过新 Key | 待最终验收 |
| 三账本与查询 | usage/request/trace app/env 请求快照，usage currency；应用/环境/未归因过滤、汇总、CSV、trace 展示 | 待最终验收 |
| 控制台 | Key/Application 身份字段、迁移入口、停用 TTL 提示；usage/request 归因查询 | 待最终验收 |
| 未归因口径 | Key 未绑定与 `unattributed=true` 业务查询分离；历史空币种/维度不回填、异步明细可能不完整 | 待核对窗口请求数/占比/费用的量化闭环，不能以 Key 数替代流量 |

**验收标准**：新企业 Key 绑定率 100%；跨租户/伪造 header/只读写入被拒绝；旧快照不变；两种入站协议与流式/非流式归因正确。Application 停用在 key cache TTL 内拒绝后续鉴权（当前默认 1 分钟），不截断在途流。

**兼容边界**：数据库 group/application 仍 nullable，仅为历史 Key 和 Desktop 保留；不自动批量补绑或收紧成 NOT NULL。Desktop nil Application 合法、不增加企业 catalog。

### E1 管控（最小周期支出管控与已有停用机制）

**目标**：应用/环境级周期成本管控，**不是严格硬预算**。预留检查原子，但实际在途费用允许超过预留/限额，耗尽后拒绝后续请求；不承诺固定最大超额。ADR-0052 的本期实施澄清优先于其原始长期 hard-budget/降级设想。

| 切片 | 当前实现 | 验收 |
|---|---|---|
| 资金五表 | policies/accounts/reservations/items/events；保留旧 quotas/充值 API，AccountingRepo 单事务协调 | 待最终验收 |
| 周期/作用域 | 日/周/月、IANA 时区、五维交集；惰性 rollover；晚结算回原账户；当前调限额/启停不清账 | 待最终验收 |
| enforce / soft | enforce 原子预留，耗尽时零估算也拒绝，实际允许超额；soft 不阻断，阈值/超额事件持久化去重 | 待最终验收 |
| 身份/定价 | 独立服务端 reservation ID 幂等；冻结 dispatcher 候选价格；同币种校验；group quota scope 统一租户/组名 | 待最终验收 |
| 未知费用与恢复 | 仅最终结果不明保留 unknown 占用；failover 成功按权威 Usage 结算并持久化 attempt_risk 事件，不积压人工队列；已持久化 result（含 usage 明细）可精确重放一次；启动及每分钟恢复/24h stale 只标待核对；全局权限带版本/证据核对，风险释放可补账 | 待最终验收 |
| API/控制台 | budget.read 租户读；budget.write/resolve 仅 global；策略/账户/事件/待核对视图、确认与双语状态 | 待最终验收 |
| Kill switch | 复用 Tenant/Application 禁用与 Key 撤销，缓存 TTL 内生效；不增加分布式即时失效 | 待最终验收 |

**本次未实现，不能勾为完成**：
- [ ] Token Allowance
- [ ] 预算驱动预设降级
- [ ] 邮件/webhook 等外部通知

**非目标**：queueing、approval、borrowing、mid-stream truncation、客户端跨 HTTP 幂等、GPU accounting；E2/E3 不随本次实施完成。

### E0/E1 交付门禁与迁移说明

- 企业迁移使用 00029/00030/00031，main 的 00028 user_agent 保留；本分支缺 00018/00028 合法，编号唯一。已执行旧企业 00028 的开发库须按合并后最终序列显式重建，不靠文件改名修复历史、不自动删除现有数据库。
- PR/main push 的 `ci-integration` 已配置 DB/迁移、数据面 E2E、Web E2E 门禁；Web 脚本使用同一隔离 PG 的 admin/gateway/mock/Next，清理仅限自身资源。
- **验收状态：已登记（本地实际运行，2026-09-30）**
  - `make ci` 通过：fmt/vet/lint(0 issues)/arch-check、`go test -race ./...`（含 Desktop canary）、check-errors(72)、check-permissions(34)、check-docs、check-frontend-permissions、SDK codegen/typecheck/lint/test/build、三个真实 stack 套件（devstack 冒烟、数据面 SDK 契约、Control Panel 契约）全部 PASS。
  - `make ci-web` 通过：check-i18n、check-i18n-keys、check-ui、web typecheck、eslint（17 warnings、0 errors）、web 单测 1924 passed、`next build` 成功。
  - `make test-db` 通过：`-tags=dbtest` 全包（含 store/admin 预算、身份、归因真实 PostgreSQL 回归）。
  - `make test-e2e` 通过：数据面 + `TestEnterprise*`（两协议×流/非流、三账本归因、伪造 header、跨租户隔离、应用停用 TTL、预算预留/结算/阈值/耗尽拒绝/soft/币种冲突、unknown 人工核对与补账幂等）。
  - `make web-e2e`（`./scripts/web-e2e.sh`）50/50 通过，含 api-keys/applications/budgets 真实 gateway 闭环。
  - 未做/环境限制：未发布镜像或安装包，未推送分支；`make ci-heavy`（Docker 构建 + 漏洞扫描）未运行；多实例/Redis 方向仍未启动。

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

### desktop 个人网关（ADR-0041 / ADR-0057）

**目标用户**：个人开发者（作者本人即用户），有多个 LLM 调用源（CodeBuddy/Codex/Claude Code/脚本），需要一个本地、轻量、无需云服务的统一入口。产品主轴是**分发 + 统计**：先让流量稳定路由和故障转移，再看清最终成功率、错误、延迟、token、Provider/Model/Agent 分布，并从请求下钻分发路径、Session Trace 与运行日志。

**优先级**：可用性 → 可解释性 → 整理能力。Prompt/completion 浏览与收藏继续保留，但不再是唯一价值主轴。

**明确排除**：多租户、RBAC、配额、跨实例一致性、Agent 专属治理、智能动态调度、主动定时探活。


**当前基线**：
- [x] SQLite store + 本地 YAML + 主入口 + Vite/Wails UI
- [x] Model/Route CRUD + 配置热重载
- [x] 请求日志、Session/Trace、Prompt 收藏、运行日志、设置与连通性测试
- [x] macOS `.app` + Windows NSIS `.exe` 打包链路
- [x] Provider UI 对齐 ADR-0049 `endpoints[]`（Batch A 修复，提交/编辑均走每端点 `id/adapter/base_url`）
- [x] 桌面 SQLite 同步共享 request/trace 新字段（ingress_protocol / provider_endpoint）

**当前演进批次**：

1. **Batch A — 可用性**：修 Provider 契约漂移；补 SQLite 字段同步；补 Provider → Model → Route → Test 四步 SetupReadiness 首页引导。**已完成**。
2. **Batch B — 可解释性**：请求级运行态首页已落地；DispatchStep observer + SQLite/API/UI 已落地；被动 ProviderHealth 已落地（Providers 页状态列）。**Batch B 已完成**。
3. **Batch C — 整理能力**：Session 收藏与整会话留存豁免；按 Session/时间/全部清理本地观测数据；Trace 设置敏感性说明与清理反馈。**Batch C 已完成**。
4. **发布准备**：在前三批达到产品可用后完成 desktop `.dmg`、面向个人开发者的安装与使用文档。**已完成**（`make desktop-build` 产出 ad-hoc 签名 `.app` + `.dmg`；用户文档见 [docs/desktop/getting-started.md](desktop/getting-started.md)；Developer ID 正式签名 + 公证、Windows 签名/ARM64 为后续可选升级）。

**复用关系**：差异仍收敛在 `internal/desktopstore`（SQLite）、`internal/desktopapi`（本地 API）、`cmd/desktop`/`internal/desktopapp`（组合与生命周期）和 `desktop-ui`。ADR-0057 仅允许在共享 Dispatcher 增加可选、fail-open、只观测不决策的 DispatchStep 契约；企业版无需同步持久化。

**编译期 canary**：任何改共享 proxy/config/auth/observability 契约的 PR 都会由 desktop 编译和 wiring test 先暴露关联影响。

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
- 2026-07-31：桌面定位收敛为“分发 + 统计”，新增 ADR-0057 与可用性→可解释性→整理能力三批演进顺序
- 2026-09-30：同步 E0 收口与最小 E1 的当前实现；明确周期支出管控允许在途超额、旧余额保留、未知费用核对与恢复；实现/最终验收分栏，Token Allowance/自动降级/外部通知及 E2/E3 仍未实现
