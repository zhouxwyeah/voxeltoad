# Step 7 拆分计划 —— PostgreSQL 持久化 + 管理面

> 数据面/管理面同构 Go 单体的最后一块：把配额、密钥、用量、配置、管理 API 落到
> PostgreSQL。决策见 ADR-0013/0014/0015/0016/0017，背景见 ADR-0005/0006/0007/0012。
>
> **铁律**：每个子步骤**测试先行**（先写失败测试 → 实现 → 绿），每步 `make ci`
> +（涉及 DB 时）`make test-db` 必绿，每步一个聚焦的 conventional commit。
> 子步骤尽量正交，按下列顺序推进（后者依赖前者的产物）。

## 全局约束

- **唯一引入 PostgreSQL 的步骤**。in-memory 实现降级为测试专用。
- **金额全用 `int64` 微单位**（ADR-0013）；缩放因子集中在 `billing` 一个常量。
- 所有 DB 测试走 embedded-postgres（`dbtest` tag），`TestMain` 调 `store.Migrate`
  —— 与 prod 同一迁移路径（ADR-0015，满足"test 走 prod 同一路径"铁律）。
- 分层：proxy / admin 均可 import `store`，proxy 不得 import admin（ADR-0016）。

---

## 接口变更总览（先于编码对齐）

这些变更横跨多个子步骤，集中列出以便实现时对齐。每项都**测试先行**。

### 1. `internal/config/schema.go` —— Pricing 改 int64 微单位
```go
// 现状（float64）
type Pricing struct {
	PromptPer1M     float64 `json:"prompt_per_1m"`     // → int64
	CompletionPer1M float64 `json:"completion_per_1m"` // → int64
	Currency        string  `json:"currency"`
}
// 目标：PromptPer1M / CompletionPer1M 为 int64 微单位。
// YAML/JSON 入站可接受人类可读小数，加载时换算为微单位（单一换算点）。
```

### 2. `internal/billing/cost.go` —— Cost 改 int64 + 确定性舍入
```go
// 现状：func Cost(u *adapter.Usage, p config.Pricing) float64 （cost.go:17）
// 目标：func Cost(u *adapter.Usage, p config.Pricing) int64
//   cost = round( tokens/1_000_000 * rate ) ，整数微单位运算，
//   round-half-up 在末尾一次性应用（ADR-0013，确定性）。
```

### 3. `internal/billing/store.go` —— QuotaStore 改 TryDebit/Settle（scope 集）
```go
// 移除：Exceeded(ctx, scope) / Debit(ctx, scope, float64)
// 新增（ADR-0016 的 scope 集形态，事务边界归 store）：
type QuotaStore interface {
	// TryDebit 在单事务内对每个 scope 条件扣减 est；任一不足 ⇒ 全回滚 ⇒ ok=false。
	// 无行的 scope 跳过（absence=unlimited）。
	TryDebit(ctx context.Context, scopes []string, est int64) (ok bool, err error)
	// Settle 对所有 scope 按 delta=est-actual 无条件结算（退/补），总是调用。
	Settle(ctx context.Context, scopes []string, delta int64) error
}
// UsageRecord.Cost: float64 → int64。
// UsageRecorder.Record 改为非阻塞入队语义（见步骤 3）。
```

### 4. `internal/plugin/plugin.go` —— Context 加拒绝状态（ADR-0013/0016）
```go
// 现状：Stop bool / BlockedBy string （plugin.go:52-53），router 统一映射 429
// 新增：拒绝状态字段，让 router 区分 402/429/503
type Context struct {
	// ...
	Stop       bool
	BlockedBy  string
	RejectStatus int // 0=默认(429)；402=配额不足；503=配额存储不可达
}
```

### 5. `internal/proxy/router.go` —— 按 RejectStatus 出码
```go
// 现状 router.go:105-106：pc.Stop ⇒ 固定 http.StatusTooManyRequests(429)
// 目标：pc.Stop ⇒ 据 pc.RejectStatus 出 402 / 429 / 503（默认 429），
//       错误类型相应映射（quota_exceeded / rate_limit_error / api_error）。
```

### 6. `internal/billing/plugin.go` —— 改用 TryDebit/Settle，Post 总结算
```go
// Pre(checkQuota): est = promptTokens + req.MaxTokens × (候选 provider 最大
//   completion 费率)；TryDebit(scopes, est)；ok=false ⇒ Stop + RejectStatus=402。
//   存储不可达(err) ⇒ Stop + RejectStatus=503（fail-closed）。
// Post(bill): actual = Cost(实际 usage, 命中 provider 定价)（无 usage ⇒ actual=0）；
//   Settle(scopes, est-actual) 【总是调用，含全额退款】；Record 入队（非阻塞）。
//   ⚠️ 删除现 plugin.go:62 的 nil-usage 早退（pre-debit 下会泄漏预留）。
```

---

## 子步骤

### 7.1 迁移基建 + schema + config_generation（ADR-0015 / 0014）

**测试先行**：`store.Migrate` 应用全部迁移后，各表存在、约束生效；
`config_generation` 种子行 = 0；重复 Migrate 幂等。

- 引入 `goose` 依赖；`internal/store/migrations/` 放首版迁移（`//go:embed`）。
- 建 ADR-0014 全部表：`providers/models/routes/plugins`（身份列 + `spec/params JSONB`）、
  `tenants/groups/api_keys/quotas/usage_records/audit_logs`、`config_generation`。
- `usage_records` 按月 range 分区；`(tenant_id, created_at)` 索引。
- `quotas(scope PK, balance BIGINT, currency, updated_at)`。
- 金额列全 `BIGINT`。
- `store.Migrate(db)` 单一入口；改造 `store_dbtest_test.go` 的 `TestMain` 调用它
  （替换裸 smoke test）。
- admin 启动自动迁移 + PG advisory lock；保留 `migrate` 子命令。

**产物**：可迁移的真实 schema + 迁移测试基建。**commit**: `feat(store): goose migrations and initial schema`

### 7.2 QuotaStore PG 实现 + 计费改造（ADR-0013 / 0016）

依赖 7.1（quotas 表）。这一步落地"接口变更 1–6"的全部。

**测试先行**（dbtest）：
- `TryDebit` 余额足 ⇒ ok=true 且原子扣减；任一 scope 不足 ⇒ ok=false 且**全部不扣**
  （事务回滚验证）；无行 scope 跳过。
- 并发 `TryDebit` 不超卖（race 测试，验证 overshoot 有界）。
- `Settle` 退/补正确；`actual=0` 全额退款。
- `Cost` int64 + 舍入确定性（表驱动）。
- 纯逻辑测试（非 dbtest）：billing plugin Pre 出 402、存储 err 出 503、Post 总结算。
- router：RejectStatus → 402/429/503 映射。

- `store` 加 **raw SQL** quota repo（`TryDebit`/`Settle`，scope 集单事务，prepared）。
- 改 `Pricing`/`Cost`/`QuotaStore`/`UsageRecord` 为 int64（接口变更 1–3）。
- 改 `plugin.Context` 加 `RejectStatus`，router 出码（接口变更 4–5）。
- 改 `billing.Plugin` Pre/Post（接口变更 6），删 nil-usage 早退。
- in-memory `QuotaStore` 同步改造为 TryDebit/Settle（仍测试专用）。
- 数据面 quota-store DSN 走 bootstrap/env（ADR-0013）。

**产物**：生产级强一致配额。**commit**: `feat(billing): pre-debit/settle quota over PG, int64 money, reject codes`

### 7.3 异步用量记录 worker（ADR-0016 / 0012）

依赖 7.1（usage_records）。

**测试先行**：
- `Record` 非阻塞入队；worker 批量刷 PG（dbtest 验证落库）。
- 缓冲满 ⇒ 丢弃 + `usage_records_dropped` 计数递增，**绝不阻塞**（fail-open）。
- Settle 与 Record 不共事务（丢弃的 record 不影响余额）。

- `store` 加 gorm `usage_records` 写入 repo。
- 加用量记录 worker（有界 channel + 批量 flush + 丢弃指标）。
- `UsageRecorder.Record` 改非阻塞入队。

**产物**：审计用量落库（fail-open）。**commit**: `feat(billing): async usage recorder with bounded buffer`

### 7.4 KeyStore PG 实现（ADR-0006）

依赖 7.1（api_keys）。

**测试先行**（dbtest）：`LookupByHash` 命中/未命中/过期/软删除（revoked_at）；
返回 `KeyRecord`（Tenant/Group/AllowedModels）正确。

- `store` 加 gorm `api_keys` repo 实现 `auth.KeyStore`。
- 接入 `Authenticator`（cache-first + 此 store 兜底，替换测试用 store）。

**产物**：生产级密钥查询。**commit**: `feat(auth): PostgreSQL KeyStore`

### 7.5 admin CRUD + 快照序列化（ADR-0014 / 0015）

依赖 7.1。把 `internal/admin` 的 `notImplemented` 占位换成真实现。

**测试先行**：
- 各资源 CRUD（providers/models/routes/plugins/tenants/groups/api_keys/quotas）。
- 跨引用校验：route→provider、upstream→provider 悬空名 ⇒ 400；quota scope 对
  tenancy 校验 ⇒ 400。
- 任意配置写入在**同事务**内 bump `config_generation`。
- 快照端点：读 `config_generation` 作 ETag；`If-None-Match` 命中 ⇒ 304；
  `spec/params JSONB` + 身份列序列化回 `config.Dynamic`，数据面 Poller 能解析
  （契约测试：序列化 → Poller 反序列化 round-trip）。
- api_keys 创建：明文一次性返回，库存 hash（ADR-0006）。

- `store` 加 gorm 各资源 repo；admin service 层 + handler。
- 快照 handler 真序列化（替换 `server.go` 硬编码 `"v0"` 与 `raw:nil`）。

**产物**：可用管理面 + 真实配置下发。**commit**: `feat(admin): resource CRUD and config snapshot serialization`

### 7.6 operator 鉴权 + RBAC + scoped-repo + bootstrap + audit（ADR-0017）

依赖 7.5（在 CRUD 之上加鉴权/授权/隔离/审计）。

**测试先行**：
- 登录：argon2id 校验、session 签发、失败锁定；登出/吊销使 session 失效。
- 授权：tenant-admin 访问全局配置 ⇒ 拒绝；super-admin 放行。
- **租户隔离**：tenant-admin 读另一租户的 keys/quotas/usage ⇒ 查不到
  （scoped-repo 强制注入 tenant_id，断言无法构造未限定查询）。
- bootstrap 子命令幂等（已有 super-admin ⇒ no-op）。
- audit：每个 mutation 写 append-only 行（operator_id/action/resource/before-after）；
  读不写审计。

- 迁移加 `operators`(email/argon2id/role/tenant_id NULL)、`sessions`、（roles 视模型大小用枚举或表）。
- authn middleware（session→operator）+ authz 层（role+tenancy）→ 发 scoped/unscoped repo。
- scoped-repository 包装（强制 tenant_id 注入）。
- `cmd/admin` 加 `bootstrap` 子命令。
- audit middleware 包裹 mutating handler。

**产物**：带 RBAC + 审计的管理面。**commit**: `feat(admin): operator auth, RBAC, tenant isolation, audit, bootstrap`

---

## 顺序与依赖

```
7.1 迁移+schema ──┬─→ 7.2 QuotaStore+计费改造（核心，接口变更集中地）
                  ├─→ 7.3 异步用量 worker
                  ├─→ 7.4 KeyStore PG
                  └─→ 7.5 admin CRUD+快照 ──→ 7.6 RBAC+鉴权+审计+bootstrap
```

7.2/7.3/7.4 相互正交（都只依赖 7.1），可独立推进；7.6 必须在 7.5 之后。

## 显式不在本步范围（各有未来 ADR / phase-2）

- provider 上游密钥**加密落库**（vs `env://` ref）—— 独立 ADR；当前
  `providers.spec.api_key_ref` 仍存 ADR-0003 引用串，不存明文密钥。
- Redis 配额后端（LiteLLM 模型）、PG RLS 纵深防御、用量 WAL 溢出零丢失、
  OIDC/SAML SSO、viewer 只读角色、跨币种 —— 均 phase-2。
- React 管理面前端（P1）。
