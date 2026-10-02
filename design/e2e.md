# E2E Testing Guide

> 端到端验证完整链路：**业务方 → TS SDK → 网关 → (mock 或真实) 上游供应商 → 流式回传**。
> 同一套测试既守护 TS SDK，也作为网关 API 的契约/回归门禁。

## 两层 E2E

| 层 | 工具 | 上游 | 何时跑 |
|---|---|---|---|
| **SDK 契约/集成测试** | vitest（在 `sdk/typescript/`） | mock 上游 | 每次 PR，CI 默认 |
| **网关集成测试** | Go `testing` + testcontainers（在 `test/`） | mock 上游（默认）/ 真实供应商（profile 开启） | PR 跑 mock；真实供应商按需手动/定时 |

## Running Tests

```bash
# SDK 集成测试（启动 mock 上游 + 网关，再用 SDK 打）
cd sdk/typescript && yarn test:e2e

# 网关集成测试（Go），默认用 mock 上游 profile
make test-e2e

# 快速冒烟：核心路径子集，不带 -race，开发内循环用
make test-e2e-quick

# 用真实供应商 profile（需要真实 key）
E2E_PROFILE_PATH=./test/profiles/real-providers.yaml make test-e2e

# 只跑某供应商
make test-e2e GREP=claude
```

## 性能：共享 embedded-postgres（关键）

E2E 慢的主因**不是** UI，而是每个用例都要拉起一整套栈。最贵的是 embedded
PostgreSQL 的 initdb + 启动（~3-4s/次）。因此 `test/e2e/main_test.go` 用
`TestMain` **按包只启动一个** embedded PG（固定端口 54330、固定 RuntimePath，只跑
一次迁移），所有用例复用它；每个用例在 `NewHarness` 里用 `truncateAll` 做
`TRUNCATE ... RESTART IDENTITY CASCADE` 重置数据，而不是各起各的实例。这把整套
E2E 从 ~4-5 分钟降到 ~1 分钟（含 -race）。

约束与注意：
- **不要**在 e2e 包里再手动 `embeddedpostgres.NewDatabase().Start()`；连 `sharedDSN`/
  `sharedDB` 即可，隔离交给 `truncateAll`。
- 因共享单实例，用例**串行**运行（不加 `t.Parallel()`）——瓶颈本就是启停成本，串行已足够快。
- `config_generation` 是单行种子表（version 0，config 写入靠 `UPDATE` bump），
  **不能** TRUNCATE，否则种子行消失、version 永不递增、快照为空；`truncateAll` 对它
  单独 `UPDATE ... SET version = 0` 重置。
- `test-e2e-quick` 通过 `-run` 选核心用例（非流式/流式 TTFT/路由/鉴权/限流/计费结算），
  开发内循环快速自检；提交/合并前跑完整 `make test-e2e`。

### 三个 stack 测试共享 PG（stack-test-all）

`test/e2e` 是 Go 包内共享（`TestMain`）；三个 **stack 测试**（devstack 冒烟 /
sdk-chat-e2e / adminstack 契约）是 **shell 脚本驱动的独立进程**，无法复用
`TestMain`。它们共享 PG 的机制是：

- `cmd/testpg`（build tag `testpg`）起**一份**固定端口 **55431** 的 embedded PG，
  共享 RuntimePath（二进制只解压一次），并 `DROP+CREATE` 两个库 `voxeltoad_devstack` /
  `voxeltoad_adminstack` 保证每次 `make ci` 干净。
- `cmd/devstack` / `cmd/adminstack` 支持 **`GATEWAY_PG_DSN`**：设置时跳过内嵌 PG，直连该
  DSN；未设置保持原自包含行为（单独 `make devstack-test` 等仍可用）。
- `scripts/stack-test-all.sh`（`make stack-test-all`，`make ci` 调用）一次 build
  三个二进制 + 起一份 PG，串行跑三个 suite。devstack 冒烟与 sdk-chat-e2e 共享同一个
  devstack 进程（同一 gateway :8080 / mock-control :8091），back-to-back 无重启。

隔离是**库级**（同 PG 不同 database），不是实例级。三个 suite 各自 seed 自己的
fixtures、断言自己的响应，不断言整库绝对行数，因此库级隔离足够。端口分区：
54329（store dbtest）/ 54330（test/e2e）/ **55431（stack-test-all）** / 5432（本地 dev）。

## 企业 E0/E1 验收（实现完成，待最终交付验收）

企业链路使用隔离 PG + mock，不接真实供应商。现有 `test/e2e/enterprise_e2e_test.go` 覆盖应用归因/两种入站/流式与非流式、停用、历史补齐、预算结算/阈值/耗尽、unknown 核对、soft 与币种冲突；细粒度资金并发和周期行为由 store/billing 测试承接。测试文件存在不代表整套已运行通过，实际执行结果由最终交付逐项登记。

| 验收面 | 必须断言 |
|---|---|
| E0 身份 | 同租户 Group/Application/env 新 Key；owner 与消费 Group 可不同；伪造 header 不改变身份；跨租户与只读角色写入拒绝；历史 Key 一次补齐不改已有非空身份 |
| 归因账本 | usage/request/trace 请求时快照与 app/env 查询、CSV 一致；旧快照不回填；usage 仅已知结算，trace 默认关闭，不能要求每个失败都造三表行；unbound Key 数与 unattributed 流量/费用区分 |
| 五维资金 | Tenant/消费 Group/Application/Application+env/Key + 旧 quota 整笔原子预留；不足/零额拒绝、并发预留不重复占用；**允许在途实际结算超额**，之后拒绝；soft 不阻断；无预算配置仍走 reservation 幂等结算 |
| 周期与价格 | 日/周/月、时区/DST、周一起算；并发 rollover 与跨期晚结算；调限额/启停不清账；请求固定 dispatcher 价格版本；币种冲突拒绝与显式零价可辨 |
| 故障与恢复 | 预留后取消/后续 Pre 拒绝；失败 attempt/断流/缺 Usage 保留 unknown；已知 result 持久化后失败可重试；stale 只标核对；风险释放再补账无双扣双退、版本冲突与事件去重 |
| 兼容与契约 | 旧 quota API、Application PATCH 对象响应、SDK codegen/contract、Web Server Actions、Desktop nil Application/canary |

### Web E2E 同库组栈

`scripts/web-e2e.sh` 的自动模式由 adminstack 持有临时 embedded PG；devstack 向该库提供本地 mock；真实 gateway 读取同一 admin 的快照并把账本写回同库，Next/Playwright 驱动管理操作与数据面验证。复用现有入口，不新增生产程序。

- 脚本显式清除继承的开发 DSN/持久化/真实演示种子配置，只从自己启动进程的 ready banner 取测试连接；仅清理本次 PID 与临时产物，保留日志以便排障。
- 自动模式 admin :8090、gateway :12800、mock stack :8080/:8091、Next 默认 :3000；端口已被占用即报错，不能误用或停止用户已有服务。仅显式 `WEB_E2E_EXTERNAL=1` 使用外部测试服务。
- `web/tests/e2e/budgets.spec.ts` 覆盖登录、平台预算生命周期/金额/版本、租户只读隔离和无权限态；应用/Key/usage/request 场景复用对应 spec。
- `ci-integration` 已配置为 PR 与 main push 均运行 `make test-db`、`make test-e2e`、`make web-e2e`，依赖 ci-light；原 main-only heavy 回归保留。门禁配置存在不等于本地或 CI 已通过。

### 迁移验收与安全边界

当前企业迁移编号为 00029/00030/00031，main 的 00028 user_agent 保留；本分支缺 00018/00028 合法，但编号不得重复。验证新建库、旧公共 00027 基线升级、新迁移隔离 Up/Down/Up。已执行旧企业 00028 的开发库不能靠重命名迁移修复 goose 历史；用户开发库需要显式重建，测试不得自动删除它。

全量交付需分别记录 `make ci`、`make ci-web`、`make test-db`、`make test-e2e`、关键 `make web-e2e` 的实际结果；不能把未执行/环境阻塞写成通过，不为过门禁跳用例或放宽契约。

## Profile YAML + 特征标志（核心机制）

借鉴 neutree 的 profile 系统：**所有环境/凭证配置集中在 YAML，按 profile 切换**，并由配置**自动推导特征标志**，无对应能力时自动跳过相关测试。这样 **CI 无需任何真实供应商 key 也能跑完整套**。

```
test/profiles/
├── default.yaml          # 全 mock 上游，占位 key，CI 默认
└── real-providers.yaml.example  # 真实供应商示例（gitignore 真实版本）
```

`default.yaml`（节选）：

```yaml
gateway:
  base_url: "http://localhost:8080/v1"
  admin_api_key: "test-admin-key"

providers:
  openai:
    base_url: "http://localhost:9101"   # 指向 mock 上游
    api_key: "sk-fake-openai"
  claude:
    base_url: "http://localhost:9102"
    api_key: "fake-claude-key"
  tencent:
    api_key: "fake-tencent-key"
  zhipu:
    api_key: "fake-zhipu-key"
```

**特征标志推导**（伪代码，配置加载层实现）：

```
hasRealOpenAI = providers.openai.api_key 不以 "fake"/"sk-fake" 开头
hasRealClaude = providers.claude.api_key != "fake-claude-key"
...
```

测试里据此跳过：

```go
if !cfg.Features.HasRealClaude {
    t.Skip("跳过真实 Claude 测试：当前 profile 未配置真实 key")
}
```

```ts
test.skipIf(!features.hasRealOpenAI)("真实 OpenAI 流式补全", async () => { ... });
```

## Mock 上游供应商（必备）

`test/mock-upstream/` 为每个供应商提供一个可控的 HTTP mock，支持：

- **非流式**与**流式（SSE）**两种响应模式
- 注入指定的 `usage` 字段（验证计费入账）
- 模拟错误：超时、429、5xx、限流（验证网关熔断/重试/降级）
- 各供应商的**协议差异**（Claude 的 SSE 事件格式 vs OpenAI 的 `data:` chunk）

每个供应商的 mock 必须能回放真实响应样本（与单测共用 `testdata/`）。

## SSE 流式断言（本项目最关键的测试）

流式转发是网关核心难点，E2E 必须覆盖：

1. **chunk 序列完整性** —— 收到的 chunk 数、顺序、首 chunk 含 role、末尾正确终止（`[DONE]`）。
2. **流式 usage 聚合** —— 流结束后计费入账的 token 数 == mock 注入值。
3. **首字延迟（TTFT）** —— 第一个 chunk 在合理时间内到达（验证未被错误缓冲攒包）。
4. **中途错误** —— 上游流到一半断开/报错时，下游错误格式正确；已知完整 Usage 按冻结价格入账，缺尾部 Usage/费用不确定则保留 unknown 占用，不伪造 token 或按零费用退款。
5. **跨供应商一致性** —— Claude（事件型 SSE）经网关转换后，下游收到的仍是 OpenAI 兼容 chunk。

```ts
test("streamed chat completion stitches chunks and bills usage", async () => {
  mockOpenAI.streamResponse({ chunks: ["Hel", "lo"], usage: { prompt: 5, completion: 2 } });
  const stream = await client.chat.completions.create({ model: "gpt-4o", messages, stream: true });
  let text = "";
  for await (const c of stream) text += c.choices[0]?.delta?.content ?? "";
  expect(text).toBe("Hello");
  const usage = await client.usage.getLatest();
  expect(usage.totalTokens).toBe(7);   // 流式聚合入账正确
});
```

## 异常路径（必测）

鉴权失败(401)、API Key 无权限(403)、限流触发(429)、配额/预算预留不足(402)、上游熔断后降级到备用供应商、敏感词拦截、缓存命中直接返回 —— 每条都要有用例。

## 测试数据隔离

- 每个创建数据的测试**自清理**。用唯一名 `` `test-${type}-${Date.now()}` ``。
- 通过 **API Helper** 直接建/删测试数据（租户/Key/配额/路由），不走 UI（UI 是 P1）。
- 可删除测试资源按**反向依赖顺序**清理；预算策略/资金账本无删除 API，由测试自己持有的隔离库在用例 reset 或退出时清理，不能为测试新增业务删除入口，更不能清空外部/开发库。
- 测试数据命名**避开** `create/edit/delete` 等词，防止与按钮/选择器文案冲突。

## Desktop 网关 e2e 模式（无 build tag）

桌面网关的"组装后真能跑通"测试（`cmd/desktop/wiring_test.go`）与企业级 `test/e2e/harness_test.go` 的 in-process 全栈范式同构,但有几点差异:

- **无 build tag、每 PR 跑**:SQLite in-process(`t.TempDir()` + WAL),<1s,不沾 embedded-postgres 的重量级栈。`e2e` tag 是为控制 PG 的启动成本;desktop 不需要。
- **企业字段边界**：默认 nil Application 合法，SQLite 无需为了企业预算增加 catalog/schema；共享 DTO 扩展需验证 nullable 字段兼容，Desktop 不装 Accounting。
- **共享契约面的运行期 canary**:in-process 装配 `proxy.Router` + desktop SQLite sinks + `config.Load` 闭包 + mock 上游(`test/testsupport/mock_upstream.go`,仅 import `net/http`+`httptest`,不拖 `internal/store`/PG)。真打 `/v1/chat/completions`(流式 + 非流式),断言 `request_logs`/`trace_payloads` 落 SQLite 且读 API(`/api/v1/*`)能取回。
- **配置 CRUD + 热重载 canary**:`internal/desktopapi/config_handlers_test.go` 通过真 API 端点增删改 provider/model/route,验证 YAML 原子写回 + `watcher.Build()` 重建 dispatcher(201 状态证明 rebuild 成功)+ 引用校验(409)+ 手动 reload。
- **编译期 + 运行期双层守卫**:编译期(`make test` 编译 desktop 三包)抓住共享接口**签名**变更;运行期(wiring_test + config_handlers_test)抓住**语义/装配/热重载**变更。

手动冒烟由 `scripts/desktop-test.sh` 提供(build → 后台 → curl → 清理),对照 `scripts/devstack-test.sh` 形态。

## Adding Tests for a New Provider

1. 在 `test/mock-upstream/` 加该供应商 mock（非流式 + 流式 + 错误注入）。
2. 在 `test/profiles/default.yaml` 加该供应商的 mock 配置（fake key）。
3. 在 `real-providers.yaml.example` 加真实配置示例占位。
4. 写 spec：非流式、流式、计费、错误路径、与 OpenAI 兼容性 5 类至少各一例。
5. 真实供应商用例用 `skipIf(!features.hasRealX)` 包裹。

## Pitfalls（持续积累踩过的坑）

> 这一节随开发推进不断补充——把每个调试半天才发现的陷阱固化成规范。

- **SSE 缓冲攒包**：转发时若用了带缓冲的 writer 而忘记 `Flush()`，会破坏流式体验（TTFT 飙升）。断言 TTFT 可暴露此问题。
- **半包/粘包**：上游 SSE 一个 `data:` 可能跨多个 TCP 包，解析器必须按 `\n\n` 边界切分而非按读取批次。mock 要能故意分片发送来覆盖。
- **usage 来源**：计费**以上游响应返回的 usage 为准**；本地 tokenizer 仅用于限流前置预估，不要用本地估算入账。
- **超时分层**：连接超时、首字超时、整体超时是三个不同配置，错配会导致长流式请求被误杀。
- **降级后的计费**：故障切换到备用供应商后，入账的 provider 字段要记实际命中的供应商，不是路由首选。
- **Playwright UI e2e 字段名耦合**：`web/tests/e2e/` 中的 `comboboxFor` 依赖表单 `<input name="...">` 的确切字段名。表单重构重命名字段（如 `adapter` → `endpoint_adapter`、`role` → `_role_select`）时必须同步更新所有 spec，否则 30s 超时。详见 `design/frontend.md` §13 Playwright e2e Pitfalls。
