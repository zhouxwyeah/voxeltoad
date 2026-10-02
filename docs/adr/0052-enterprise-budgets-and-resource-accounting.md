# ADR-0052: Enterprise budgets and resource accounting

- Status: Accepted
- Date: 2026-07-31
- Builds on: [ADR-0012](0012-billing-and-quota.md) (cost and quota), [ADR-0013](0013-quota-data-plane-access.md) (pre-debit/settle), [ADR-0016](0016-data-service-design.md) (multi-scope atomicity), [ADR-0051](0051-application-governance-identity.md) (Application identity)

## Context

The current Quota mechanism is a strongly consistent, non-recurring cost balance
scoped to Tenant, Group, or APIKey. It already reserves an estimated maximum cost
before dispatch and settles to actual upstream Usage after completion. This is a
sound enforcement primitive, but it does not express enterprise calendar budgets,
application/environment scopes, soft thresholds, pre-approved degradation, or a
separate token allowance.

"Compute" also has two different meanings. For provider APIs the authoritative
facts are provider-reported tokens and billed prices. For self-hosted inference,
GPU occupancy, queue time, KV-cache behavior, and accelerator capacity are separate
resource facts that cannot be reconstructed accurately from token counts.

A single generic "token quota" would therefore conflate spend, throughput, and
physical compute while losing the strong consistency guarantees of the current
money path.

## Decision

### Cost is the authoritative enterprise budget unit

Application cost control uses integer micro-unit **Cost Budget** as its primary
hard economic boundary. Actual cost is still computed from provider-reported Usage
and the price of the provider/model that served the request.

A Budget may additionally carry an independent **Token Allowance** for capacity or
contractual controls. Cost and tokens are not interchangeable: different models,
providers, input/output directions, cached tokens, and modalities have different
prices.

RPM, TPM, and concurrency limits remain rate/capacity controls. They are not Budget
accounts and do not share calendar-budget semantics.

### BudgetPolicy and BudgetAccount separate configuration from runtime state

The management model has two concepts:

- **BudgetPolicy** — scope, measure, period, hard/soft behavior, warning thresholds,
  timezone, and overage action;
- **BudgetAccount** — one concrete policy period with limit, reserved, committed,
  released, and period start/end state.

A BudgetPolicy supports calendar `daily`, `weekly`, and `monthly` periods plus
`non_recurring`. Period boundaries are evaluated in the policy's configured
timezone; DST transitions follow that timezone's civil rules. Period rollover
creates or activates a new account; it is not a sliding-window recovery mechanism.
Rollover does not carry over unspent balance unless the policy explicitly defines
a carry-forward rule; the default is zero carry-forward.

Settlement is idempotent: a request may be settled at most once per account, keyed
by its request identity. Late-arriving settlements (e.g. delayed upstream Usage)
charge the account that was active at the request's reservation time, not the
account current at settlement time, so a period boundary never shifts spend
between accounts.

The current Quota balance maps to a non-recurring Cost Budget account. Existing
`TryDebit`/`Settle` behavior is the implementation foundation for reservation and
settlement; this ADR does not weaken its atomicity or fail-closed money semantics.

### Soft and hard budgets have distinct effects

- A **Soft Budget** emits threshold and exceeded events but never blocks a request.
- A **Hard Budget** requires a successful reservation before dispatch.

Hard-budget exhaustion follows a bounded first-phase action sequence:

1. apply an Application-configured, pre-approved degradation tier;
2. re-evaluate the route and reservation under the degraded constraints;
3. reject if no allowed tier fits the remaining budget.

A degradation tier may select a cheaper route or reduce an output ceiling. The
gateway must not invent a substitute model or silently cross an Application's
quality/policy boundary. Budget-driven degradation is recorded separately from
failure-driven failover in the routing decision ledger.

Queueing, human approval, borrowing, and mid-stream truncation are not first-phase
Budget actions. They require Agent/Harness lifecycle semantics.

### All applicable hard budgets form an intersection

A request must satisfy every configured Hard Budget that applies to its context:

- Tenant;
- consuming Group;
- Application;
- Application + environment override;
- APIKey.

All applicable reservations are all-or-nothing in one store transaction. A more
specific policy cannot override or bypass a broader hard boundary. Soft policies
are evaluated independently and only produce events.

Application ownership does not transfer spend: an Application's owning Group is
not charged for requests authenticated by another consuming Group unless it is
also the consuming Group.

This extends ADR-0016's multi-scope transaction from a tenancy-only hierarchy to a
set of orthogonal governance dimensions.

### Token allowance is independent from cost reservation

Token Allowance uses provider-reported actual Usage as the accounting fact. The
first phase does **not** perform a strict pre-dispatch token reservation, because
the gateway has no authoritative prompt tokenizer before dispatch and a fabricated
estimate would be unsafe as a hard gate.

Instead, a Hard Token Allowance is enforced as post-completion accounting: a
completed request debits actual total tokens from the current period account. An
in-flight request is allowed a bounded overage; once the period account is
exhausted, subsequent requests are rejected until the next period rolls over. This
means Token Allowance is a period-level circuit breaker, not a per-request
reservation.

No locally fabricated token count may replace provider-reported Usage in the final
ledger. A future provider-specific tokenizer may tighten this into a reservation
model, but it cannot redefine final accounting.

### Provider billing and self-hosted compute are separate resource sources

Define a conceptual **ResourceUsage** envelope with an explicit source:

- `provider_billed` — provider/model, input/output/cache/reasoning usage where
  available, price version, billed/allocated cost, latency, and status;
- `self_hosted_compute` — deployment/replica, accelerator class, GPU time, queue
  time, KV-cache signals, and internally allocated cost where available.

Only reliable source measurements are recorded. Token count must not be presented
as GPU consumption. Internal cost allocation for self-hosted compute requires an
explicit pricing/allocation rule and must be distinguishable from an external
provider bill.

This phase defines the boundary but implements only provider-billed token and cost
facts. Self-hosted compute collection and resource-aware replica scheduling are
deferred until a real vLLM/llm-d or equivalent inference plane is integrated.

## E0/E1 实施澄清（2026-09-30）

以下是本次已实现切片的有效语义，覆盖上文概念设计中与之冲突的“strict hard boundary / replace Quota / first-phase degradation”表述；原决策保留为后续演进背景。**实现完成，待最终交付验收**，不能据此宣称所有长期设计均已落地。

- **周期支出管控，不是严格硬预算**：当前 mode 为 `soft` / `enforce`。enforce 在请求前按 `available = limit - committed - reserved` 原子检查预留，所有适用账户及旧 quota 整笔成功或回滚；`available <= 0` 时即使估算为零也拒绝新请求。预留采用输出估算，不伪造 prompt Token，不构成全成本上界。实际费用超过预留仍照实入账，允许账户超额，耗尽后拒绝后续请求；不承诺总费用严格不越界或固定最大超额。soft 同样记账并产生阈值/超额事件，但不阻断。
- **保留旧 quota**：`quotas`、充值和余额读 API 不变，不把旧余额搬成 `non_recurring` 新账户。新增 `budget_policies` / `budget_accounts` / `billing_reservations` / `billing_reservation_items` / `budget_events` 五表；`billing.AccountingStore` 的 PG 实现 `AccountingRepo` 在同一资金事务协调旧余额与新周期账户，不能同时再调用旧 TryDebit/Settle 重复扣款。Group scope 统一为转义后的 `group:<tenant>/<group>`，歧义旧项须核对，不能按 unlimited 忽略。
- **本期只有成本日/周/月预算**：IANA 时区（默认 UTC），周一起算；账户惰性创建，不靠定时清零。请求固定原周期账户，晚到结算不移到新周期。只允许带 version 修改限额/启用状态；当前账户保留 committed/reserved，历史账户不改；无硬删除及原地 scope/币种/周期修改。新策略从创建后接单开始记账，不从异步历史 usage 猜测追溯基线。
- **五维交集**：Tenant、消费 Group、Application、Application+environment、Key。Group/Application 使用经租户仓储验证的实体 ID，Key 使用公开 key_id；Application owner 不自动承担其他消费 Group 的费用。`budget.read` 可读自己的租户；`budget.write` / `budget.resolve` 同时要求平台 global scope。平台查询/写入也显式选租户，不开放账户余额 PATCH。
- **独立资金幂等键和价格快照**：每次真实请求创建独立服务端 reservation ID，gateway request_id 仅关联，client_request_id 绝不去重两次 HTTP 调用。预留、实际命中计价使用同一 dispatcher 配置快照的候选价格/缓存倍率，不在 Post 重新读实时价格。所有可达候选、旧余额和预算账户币种须一致，不换汇；充值不能改变已有余额币种。显式零价不等于缺失价格。
- **未知费用不退款，但 failover 不积压**：未外呼或已证实零费用可释放；已知 Usage 按冻结价格结算。仅当**最终结果**不明确（最后一次 attempt 超时/传输失败、断流缺尾部 Usage、崩溃）才保留 unknown 占用和理由。可重试 attempt 失败后 failover 成功的请求，以成功 attempt 的权威 Usage 正常结算为 known，先前 attempt 的残余暴露作为 `attempt_risk` 事件持久化（`budget_events` kind=`attempt_risk`），不占用 unknown、不进人工核对队列；终端 5xx（无可用 fallback）仍为 unknown。已持久化 result 可安全重试；`internal/app/stores.go` 启动恢复循环并每分钟重试 pending 结果、将超过 24 小时的 reserved/dispatched 标为待核对，绝不 TTL 自动退款。
- **usage 明细随结算事务落库**：已知结果的 usage 行在 reservation 的资金应用事务内持久化（usage_records），与 `result_applied` 同事务原子生效；崩溃后由恢复循环从持久化 result 精确重放一次，异步 recorder 不再承担已知结算的明细写入（legacy 非 accounting 路径仍走 fail-open 异步）。手工核对 settle（无 Usage 载荷）不伪造明细行，成本事实以 reservation/account 为准。
- **人工核对**：全局资金权限、version 检查、操作者、理由与证据在同一事务留事件。支持有证据的实际费用 settle、证实零费 release、承担风险的 release_unknown；后者仍是费用未知，保留 `released_unknown` 并允许后续补账，按 held/committed/released 累计差额避免重复扣退。结果尚未持久化即崩溃仍有 unknown 窗口，不宣称精确零丢失恢复。
- **对账与展示**：财务事实以 reservation/account 为准，异步 usage 是分析明细而非唯一对账账本；历史缺币种保留未知，不按当前币种回填。阈值按实际 committed/limit 触发并按账户/阈值去重，reserved/unknown 单列。多个维度的账户不可求和当总消费；旧余额与周期剩余分别展示。
- **本期不实现**：Token Allowance、预算自动降级、外部通知、E2 路由决策账本、GPU accounting、客户端跨 HTTP 请求幂等。停用复用 Tenant/Application 禁用与 Key 撤销，在鉴权缓存 TTL 内生效（当前默认 1 分钟），不增加分布式即时失效，不截断已开始的流。

Schema 精确定义见 `design/database.md` §3.6；接口以 `docs/openapi/admin.yaml` 为准；当前实施与验收状态见 `docs/roadmap.md`。

## Consequences

- Application and environment become first-class Cost Budget scopes without
  weakening Tenant/Group/Key caps.
- Enterprise operators can express recurring departmental/application budgets and
  one-off funded balances through one policy/account model.
- Cost, token allowance, rate limiting, and physical compute remain auditable as
  different measures rather than one overloaded quota number.
- The reservation store must persist policy-period identity and reserved/committed/
  released state, and atomically reserve every applicable hard account.
- Budget-driven degradation must run before final reservation and routing, and its
  decision/reason must be queryable.
- Period rollover, idempotency, concurrency, and late settlement need explicit
  implementation tests before schema work lands.
- Self-hosted GPU accounting does not block the provider-API enterprise roadmap,
  while the event boundary avoids redesign when that integration becomes real.

## Alternatives considered

### Token as the primary budget

Rejected because raw tokens are not economically comparable across models,
providers, directions, modalities, and cache/reasoning pricing.

### Cost and tokens as one fungible account

Rejected because conversion would depend on a route and price not known at ingress
and would hide whether a limit protects spend or capacity.

### Keep only the current non-recurring Quota balance

Rejected because external jobs would need to top up every calendar period and the
management plane could not express soft thresholds or period-specific reporting.

### Replace Quota with calendar budgets only

Rejected because prepaid, project-specific, and one-off funded allowances still
need a non-recurring account.

### Most-specific policy overrides parent policy

Rejected because an Application or APIKey policy could then bypass a Tenant or
Group hard budget.

### Full overage workflow engine in the gateway

Rejected for the first phase. Queueing, approval, borrowing, and callbacks belong
to a broader Harness lifecycle rather than the synchronous model gateway path.

### Infer GPU cost from tokens

Rejected because batching, hardware, quantization, queueing, cache reuse, and
serving implementation make token count an unreliable proxy for physical compute.
