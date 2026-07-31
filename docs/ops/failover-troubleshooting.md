# 故障转移（Failover）请求排查指南

> 适用场景：在 session 日志 / 请求审计里看到「部分请求发生了故障转移」，需要定位对应的请求、判断影响范围、并弄清「为什么转移」。
> 本文**仅使用 Admin API**（`GET /api/v1/request-logs` 系列），不涉及任何代码改动。

---

## 1. 背景：failover 在系统里是怎么记录的

故障转移由数据面 `internal/proxy/dispatcher.go` 编排：按候选供应商顺序逐个尝试，只有**可重试错误（超时 / 5xx）**才会转移到下一个候选；**4xx 不触发转移**（见 `docs/adr/0011-routing-and-failover.md`）。

每一次请求都会写入审计表 `request_logs`（`internal/store/requestlog.go`），与 failover 相关的字段：

| 字段 | 含义 | 取值说明 |
| --- | --- | --- |
| `fallback` | 兼容用粗粒度候选索引信号 | 当前为“请求在 breaker 过滤后的候选索引 `i > 0` 处终止”；早期位置可能只是缺 forwarder/配置不匹配，熔断过滤和耗尽路径也不能稳定表达，不能等同于精确真实 failover |
| `provider` | **最终命中**的供应商 | 只记最终落点，不记逐跳过程 |
| `error_type` | 最终失败分类 | 客户端最终收到错误时非空；既可能是不可重试错误立即终止，也可能是可重试候选耗尽 |
| `request_id` | 网关生成的关联 ID（ADR-0050） | 单行精确下钻；客户端原值另存 `client_request_id` |
| `session_id` | 会话标识（`X-Voxeltoad-Session` 等） | 串起同一会话的多条请求 |
| `trace_id` | W3C `traceparent` 链路 ID | 跨请求串分布式链路 |
| `session_source` | 会话来源标签 | `header-config` / `header-generic` / `body-session` / `body-metadata` / `body-user` / `prefix` |
| `duration_ms` / `ttft_ms` | 耗时 / 首字节延迟 | 转移会显著拉高这两个值 |

### ⚠️ 关键限制（决定排查能走多远）

- **`fallback=true` 且最终成功**：只能确认请求在过滤后的候选索引 `i > 0` 处成功，不能证明前序候选真的发起过网络调用；它可能是可重试失败，也可能是缺 forwarder/配置不匹配。逐步原因当前不落库，只能结合最终 provider、候选配置、运行日志和熔断快照间接推断（见 §5）。
- **failover 彻底失败**（`error_type` 非空）：系统会额外打一条 `upstream request failed` 的 ERROR 日志（含 `request_id`/`session_id`/`model`/`provider`/`error_type`），且 `error_type` 字段可读，这是最直接的排查入口。
- `retry_count` 当前实际也是候选索引/候选数的粗信号，不等于真实网络尝试次数，且 **`request_logs` 未单独落列**。仅靠 `fallback` + 最终 provider 无法精确还原尝试次数。

---

## 2. 准备：认证与变量

Admin API 使用 `Authorization: Bearer <token>`（见 `internal/admin/rbac.go`）。

```bash
# 1) 登录拿 token（具体登录端点/参数以你的部署为准，典型为）：
curl -s -X POST "$ADMIN/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"...","password":"..."}'   # 取响应里的 token

# 2) 设定环境变量
export ADMIN="https://<admin-host>"
export TOKEN="<bearer-token>"

# 便捷函数：带鉴权调用
req() { curl -s -H "Authorization: Bearer $TOKEN" "$ADMIN$1"; }
```

> 时间范围参数 `from` / `to` 对所有列表接口生效，格式通常为 RFC3339（如 `2026-07-10T00:00:00Z`）。未指定时按服务端默认窗口。

---

## 3. 场景 A：从 session 日志里看到「部分请求发生了故障转移」

现有 session 日志把 `fallback=true` 作为“候选位置发生位移”的粗筛选信号；它适合缩小排查范围，但不能单独证明发生了真实上游 failover。

### 步骤 1 — 拉出该 session 的完整时间线
```bash
req "/api/v1/request-logs/sessions/{session_id}"
```
返回该 session 按时间 **ASC** 排列的全部请求 + 来自 `usage_records` 的成本汇总。
- 先看每行 `fallback` 字段筛出候选位置位移，再结合运行日志判断是否真的发起过多个上游调用。
- 对比 `provider`：转移的请求 `provider` 是否偏离该 session 的主供应商。

### 步骤 2 — 只筛出该 session 内发生过转移的请求
```bash
req "/api/v1/request-logs?session_id={session_id}&fallback=true"
```
快速得到该 session 的「转移子集」，便于统计转移比例与落点分布。

### 步骤 3 — 检查是否有「彻底失败」的请求
```bash
req "/api/v1/request-logs?session_id={session_id}&error_type=upstream_error"
req "/api/v1/request-logs?session_id={session_id}&error_type=timeout_error"
```
这些就是 **failover 耗尽、真正对用户报错** 的请求，带 `error_type` 与失败 provider，是需要优先处理的。

---

## 4. 场景 B：从一条具体请求（request_id）下钻

如果你已经从日志/告警里拿到了 `request_id`：

```bash
req "/api/v1/request-logs?request_id={request_id}"
```
精确命中单行，重点看：
- `fallback`：是否在过滤后的非首候选位置终止（仅粗筛，不等于真实转移）
- `provider`：最终命中谁
- `error_type`：为空 = 转移后成功；非空 = 彻底失败
- `duration_ms` / `ttft_ms`：转移通常会明显升高
- `trace_id`：用于串链路（见下）

### 用 trace_id 串起分布式链路
> **限制**：`/request-logs` 当前**未把 `trace_id` 作为过滤参数**暴露（见 `internal/admin/requestlog_handlers.go` 与 `docs/openapi/admin.yaml`）。变通做法：

1. 先用 `request_id` 取到该行的 `trace_id`；
2. 导出 CSV 后在本地按 `trace_id` 过滤同链路请求：
   ```bash
   req "/api/v1/request-logs?format=csv" > logs.csv
   grep "{trace_id}" logs.csv
   ```
   CSV 含 `trace_id` / `session_id` / `request_id` / `fallback` / `error_type` 等全字段（2000 行上限）。

---

## 5. 场景 C：批量看某时段 / 某供应商的转移情况

### 全量转移（某时间窗）
```bash
req "/api/v1/request-logs?fallback=true&from=2026-07-10T00:00:00Z&to=2026-07-10T23:59:59Z"
```

### 某供应商视角
- 看某供应商**承接了多少转移流量**（作为落点）：
  ```bash
  req "/api/v1/request-logs?provider={provider}&fallback=true"
  ```
- `provider` + `fallback=true` 只能显示哪些供应商经常在后续候选位置终止请求；不能单独判断前序供应商是网络失败、配置跳过还是熔断过滤。

### 离线分析（CSV）
```bash
req "/api/v1/request-logs?fallback=true&format=csv" > failovers.csv
```
字段含：`id,tenant,group_name,api_key_id,provider,model_requested,model_resolved,stream,prompt_tokens,completion_tokens,total_tokens,ttft_ms,duration_ms,error_type,blocked_by,fallback,request_id,client_request_id,session_id,trace_id,session_source,agent_type,cache_hit,cache_tier,cache_source,cached_prompt_tokens,upstream_request_id,ingress_protocol,provider_endpoint,created_at`。

---

## 6. 关联上游健康：解释「为什么转移」

failover 的触发根因是第一个真实上游尝试发生**可重试的连接错误 / 5xx / 超时**。熔断候选会在真实尝试前被过滤，属于 preferred-candidate bypass；当前 `fallback` 字段不一定反映这种跳过。用以下方式核实：

### 查看数据面熔断状态（super-admin）
```bash
req "/api/v1/data-plane-nodes"
```
返回各实例的 `breaker_states`（JSONB），按 ADR-0049 的 endpoint 粒度记录，形如 `"<provider>/<endpoint>": "open" | "half-open" | "closed"`：
- 首选 endpoint 为 `open` → 冷却期内会在 `filterHealthy` 阶段被跳过；这是首选绕过，不等同于 `fallback=true`。
- `half-open` → 冷却期已过，重新允许真实调用，不会被 `filterHealthy` 跳过；当前没有单探针并发门控，多条并发请求都可能进入。成功后 closed，失败则重新 open。
- 全部 endpoint 均为 open 时系统会兜底保留原候选并尝试，避免无路可走。

### 结合转移类型推断
- `error_type` 为空 + `fallback=true` → 后续候选位置成功；前序位置可能是 5xx/超时，也可能只是配置跳过，需结合运行日志确认。
- `error_type=upstream_error` → 最终错误分类为上游错误，可能是不可重试错误立即终止，也可能是多个可重试失败后耗尽；当前无法仅凭单行证明每一步都为 5xx。
- `error_type=timeout_error` → 最终错误分类为超时；当前无法仅凭单行证明所有候选都超时。

> 语义细节见 `docs/adr/0011-routing-and-failover.md`：仅超时/5xx 触发转移，4xx（含 429/401/403/内容审查）**不触发**——所以如果某请求 `error_type=4xx` 类且 `fallback=false`，那不是 failover，是上游直接拒绝。

---

## 7. 排查决策树（速查）

```
看到 "部分请求故障转移"
        │
        ├─ 有 request_id？── 是 ─→ §4 下钻单行（看 fallback/provider/error_type/耗时）
        │
        ├─ 有 session_id？── 是 ─→ §3 拉 session 时间线
        │       ├─ fallback=true 但 error_type 空 → 后续候选位置成功，结合运行日志确认真实 failover
        │       └─ error_type 非空 → 彻底失败，优先处理，§5 查根因
        │
        └─ 只有时间段？──── 是 ─→ §5 批量筛 fallback=true，CSV 离线分析
                                      + §6 查 data-plane-nodes 熔断态
```

---

## 8. 已知盲点与后续增强（仅供参考，不在本文范围）

- **逐跳明细缺失**：一次 failover 里「哪个 provider 先失败、为何重试」当前不落库，failover 成功时看不到中间过程。ADR-0051 已为 desktop 接受独立有序 `DispatchStep`（区分候选跳过与真实上游尝试）及可选 fail-open observer；功能落地前仍需按本文现有方法间接排查。
- **retry_count 未落 `request_logs` 列且当前不是精确 attempt 数**：现阶段无法仅靠 `fallback` + provider 推断真实网络尝试次数；ADR-0051 落地后从 attempted DispatchStep 精确得到尝试序列，同时保持 `request_logs` 一请求一行。
- **trace_id 未作为查询参数**：当前需用 CSV 导出后本地 grep 变通。

---

## 参考文件
- `internal/admin/requestlog_handlers.go` — 请求日志 API 入口（`/request-logs`、`/request-logs/sessions/{id}`、CSV 导出）
- `internal/store/requestlog_query.go` — 过滤字段（`fallback` / `session_id` / `request_id` / `error_type` 等）与查询实现
- `internal/proxy/dispatcher.go` — failover 编排、`Fallback` / `RetryCount` 定义
- `docs/openapi/admin.yaml` — Admin API 契约（搜索 `/request-logs`）
- `docs/adr/0011-routing-and-failover.md` — 故障转移语义权威定义（触发白名单、流式边界、计费）
