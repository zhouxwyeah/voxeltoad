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
4. **中途错误** —— 上游流到一半断开/报错时，网关向下游传递的错误格式正确，且已消耗 token 仍入账。
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

鉴权失败(401)、API Key 无权限(403)、限流触发(429)、配额超限(429)、上游熔断后降级到备用供应商、敏感词拦截、缓存命中直接返回 —— 每条都要有用例。

## 测试数据隔离

- 每个创建数据的测试**自清理**。用唯一名 `` `test-${type}-${Date.now()}` ``。
- 通过 **API Helper** 直接建/删测试数据（租户/Key/配额/路由），不走 UI（UI 是 P1）。
- 清理按**反向依赖顺序**（policy → role → tenant），失败容错（`catch`/`defer` 忽略删除错误）。
- 测试数据命名**避开** `create/edit/delete` 等词，防止与按钮/选择器文案冲突。

## Desktop 网关 e2e 模式（无 build tag）

桌面网关的"组装后真能跑通"测试（`cmd/desktop/wiring_test.go`）与企业级 `test/e2e/harness_test.go` 的 in-process 全栈范式同构,但有几点差异:

- **无 build tag、每 PR 跑**:SQLite in-process(`t.TempDir()` + WAL),<1s,不沾 embedded-postgres 的重量级栈。`e2e` tag 是为控制 PG 的启动成本;desktop 不需要。
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
