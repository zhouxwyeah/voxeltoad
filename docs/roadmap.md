# voxeltoad Roadmap

> 本文档是项目演进的**单一事实来源**，明确 P0/P1/P2 及触发条件。
> 更新日期：2026-07-17

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
- [x] **OpenAPI 契约**：43 个端点，server 端实现度高；SDK codegen 用 `git diff --exit-code` 强制同步
- [x] **数据库**：23 个迁移文件、24 张表、月度分区；database.md 与 migrations 同步纪律好
- [x] **前端控制台**：Next.js 16 + React 19 + RSC，20 个 dashboard 页面全部「真实可用」档
- [x] **SDK**：`@voxeltoad/gateway-sdk` 双产物（数据面 client + 管理面 admin），web 强依赖
- [x] **测试**：~101 个 _test.go、test/e2e/ 16 个文件、`make ci` 含 16 个 step
- [x] **CI**：GitHub Actions 双 job（ci-light / ci-heavy）

---

## P1 当前主线

### desktop 个人网关（ADR-0041）

**目标用户**：个人开发者（作者本人即用户），有多个 LLM 调用源（CodeBuddy/Codex/Claude Code/脚本），需要本地 `127.0.0.1` 收敛入口 + **被动录制所有 prompt/completion 用于学提示词**。

**明确排除**：多租户、RBAC、配额、跨实例一致性。

**当前状态**：
- [x] SQLite store + 配置 + 主入口 + UI 骨架
- [x] provider/model/route CRUD + 热重载
- [x] Wails v2 工程 + macOS .app target
- [ ] **desktop .dmg 发布准备**——面向个人开发者发布安装包 + 使用文档

**复用关系**：`internal/proxy` / `internal/adapter` / `internal/auth` / `internal/observability` / `internal/config` **零改动复用**；差异收敛在 `internal/desktopstore`（SQLite 替代 PG）、`internal/desktopapi`（无 RBAC 读 API）、`cmd/desktop`（组合根）。

**编译期 canary**：任何改 `internal/proxy` 的 PR 都会被 desktop 编译失败先撞到。

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
