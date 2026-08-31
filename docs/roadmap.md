# voxeltoad Roadmap

> 本文档是项目演进的**单一事实来源**，明确 P0/P1/P2 及触发条件。
> 更新日期：2026-07-31

---

## 项目定位

**个人作品 + 企业愿景**——desktop 是主线（个人用 + 编译期 canary），企业版保持维护态，多实例等企业客户出现再启动。

---

## P0 已完成

以下能力已生产可用，非骨架：

- [x] **协议适配**：OpenAI / Claude 完整 adapter（含 SSE 流式），tencent/zhipu 通过 openai adapter 走配置分支
- [x] **配额计费**：Pre 预扣 + Post 结算，失败退还；token 来自上游 response.usage（非估算）
- [x] **限流**：单机内存 sliding-window，多实例靠"总额除以在线节点数"妥协
- [x] **审计**：管理面 `rbac.auditMutation` 中间件统一拦截非 GET 写操作；数据面每请求落 `request_logs`
- [x] **多租户**：middleware 层强制，handler 不重复判
- [x] **provider_credentials 加密**：AES-256-GCM 已落地（ADR-0031）
- [x] **OpenAPI 契约**：45 个 path、64 个 HTTP operation，server 端实现度高；SDK codegen 用 `git diff --exit-code` 强制同步
- [x] **数据库**：26 个迁移文件、21 张业务表 + 2 张 goose 隐式表、月度分区；database.md 与 migrations 同步纪律好
- [x] **前端控制台**：Next.js 16 + React 19 + RSC，20 个 dashboard 页面全部「真实可用」档
- [x] **SDK**：`@voxeltoad/gateway-sdk` 双产物（数据面 client + 管理面 admin），web 强依赖
- [x] **测试**：145 个 `_test.go`、`test/e2e/` 20 个文件；`make ci` 覆盖 Go、契约、前端门禁与 stack tests
- [x] **CI**：GitHub Actions 三 job（ci-light / ci-heavy / desktop-windows-build）

---

## P1 当前主线

### desktop 个人网关（ADR-0041 / ADR-0051）

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
4. **发布准备**：在前三批达到产品可用后完成 desktop `.dmg`、面向个人开发者的安装与使用文档。

**复用关系**：差异仍收敛在 `internal/desktopstore`（SQLite）、`internal/desktopapi`（本地 API）、`cmd/desktop`/`internal/desktopapp`（组合与生命周期）和 `desktop-ui`。ADR-0051 仅允许在共享 Dispatcher 增加可选、fail-open、只观测不决策的 DispatchStep 契约；企业版无需同步持久化。

**编译期 canary**：任何改共享 proxy/config/auth/observability 契约的 PR 都会由 desktop 编译和 wiring test 先暴露关联影响。

### UI 产品级化（2026-07 启动）

短期目标：控制台与 desktop-ui 从「功能真实可用」提升到「体验产品级」。规范与门禁先行，
缺口分批收敛，单一事实来源是 [design/design-system.md](../design/design-system.md)（§1 设计
参照系 / §8 缺口清单 / §9 门禁）。

- [x] **P0（2026-07-18 完成）**：设计规范升级（Sentry/Linear 参照系、五条硬性规则）+
  `make check-ui` 门禁上线；Modal 布局规范化（表单操作栏固定底部、xl 尺寸档、DetailField
  基元）；usage 图表色值修复；config/history diff/preview 404 修复；品牌 logo 落地；
  emoji/字符画图标清零（lucide 化）；roles 收敛 ConfirmModal；`dark:` 死类清零；
  非白名单彩虹色 token 化
- [ ] **P1 一致性批次**：trace 彩虹色收敛（web/desktop 两份分叉副本，评估合并）、
  Badge/EmptyState 全量推广、表格 4 变体收敛 §4.2、详情页模板推广（5 个既有详情页）、
  原生 `<select>` 残余迁移（7 处）、FilterField 抽取、i18n 硬编码文案、语言切换 UI、
  loading.tsx 按需铺设
- [ ] **P2 韧性批次**：全局 error.tsx/not-found.tsx、usage 静默吞错修复、overview 弱类型、
  spacing/radius/typography scale token 化

**约束**：`make check-ui` 已挂入 CI，白名单只减不增；新增 UI 基元/token/模板必须同步
design-system.md（CODEBUDDY.md 门禁条款）。

---

## P2 等触发

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

**状态**：Proposed，ABI v1 契约已完备（178 行，`execute(ptr, len) -> i64` 函数签名、host/guest JSON payload schema、内存管理约定、能力清单）。

**触发条件**：高级用户/企业客户真实需求。

### 5 个插件挂主链

| 插件 | 代码完整度 | 状态 | 触发条件 |
|---|---|---|---|
| cache | **不完整**（只实现 Cache 接口） | 孤儿代码 | 真需要响应缓存时 |
| sensitive | 完整 | 已 Register，未挂链 | 企业客户合规审查需求 |
| pii | 完整 | 已 Register，未挂链 | 企业客户合规审查需求 |
| injection | 完整 | 已 Register，未挂链 | 企业客户安全需求 |
| moderation | 完整 | 已 Register，未挂链 | 企业客户内容审核需求 |

**统一挂载方案**：写通用 `plugin_loader`（~80 行），遍历 `cfg.Plugins` 调 `plugin.New(name, params)`。

### Helm chart 完整化

**当前状态**：`deploy/helm/` 真实可部署但功能有限（Chart.yaml + 5 个模板 + values.yaml 70 行，仅覆盖 gateway 单入口、无 admin 部署、无 ingress、无 HPA、无 PG/Redis 子 chart）。

**触发条件**：多实例上线。

---

## Deferred

以下方向**明确推迟，无时间表**：

- 无

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

---

## 更新记录

- 2026-07-17：初版，基于 grill session 拍板结果
- 2026-07-18：新增「UI 产品级化」批次（P0 完成，P1/P2 排期）；design-system.md 升级为视觉单一事实源 + `make check-ui` 门禁
- 2026-07-31：桌面定位收敛为“分发 + 统计”，新增 ADR-0051 与可用性→可解释性→整理能力三批演进顺序
