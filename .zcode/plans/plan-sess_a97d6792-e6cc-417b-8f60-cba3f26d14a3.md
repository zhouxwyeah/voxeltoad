## 修复目标

让 code agent 经 gateway 时工具调用正常工作：客户端传入的 `tools`/`tool_choice` 能送达上游，上游返回的结构化 `tool_calls`（含流式增量）能完整透传回客户端。当前缺失导致模型退化为 `<｜｜DSML｜｜tool_calls>` 文本、agent 不执行任何动作。

## 根因（已确认）

1. **请求侧**：`UnifiedRequest`（`internal/adapter/adapter.go:50-63`）无 `Tools`/`ToolChoice` 字段，`Extra` 标了 `json:"-"` 不会被解码；OpenAI 适配器 `wireRequest`（`internal/adapter/openai/adapter.go:57-64`）也不含。客户端 `tools` 在 decode 阶段即被丢弃，上游收不到工具定义，只能用文本拼工具调用。全仓 `grep "tools"` 非测试代码零命中。
2. **响应侧（流式）**：`adapter.Chunk`（`adapter.go:90-97`）、`openai.wireStreamChunk.Delta`（`openai/adapter.go:131-134`）、`proxy.wireStreamDelta`（`proxy/stream.go:37-40`）三处都只有 `Role/Content`，即使上游正确返回 `delta.tool_calls` 也会被静默丢弃。非流式因 `adapter.Message` 已有 `ToolCalls` 字段且 `ParseResponse` 直接 unmarshal，已能保留（有 `TestParseResponse_WithToolCalls` 覆盖）。

注：`normalize.Apply` 用浅拷贝 `out := *req`，新增的 `Tools`/`ToolChoice` 会自动透传，无需改 normalize/preparer 层。

## 实现步骤（仅 OpenAI 适配器；Claude 适配器保持现状，为已知限制）

### 1. 请求侧：透传 tools / tool_choice

**`internal/adapter/adapter.go`** — 新增类型与字段：
- 新增 `Tool` 类型：`{Type string; Function FunctionDef}`，`FunctionDef{Name, Description, Parameters json.RawMessage}`（`Parameters` 用 `json.RawMessage` 原样透传 schema，避免 gateway 关心 schema 内部结构）。
- `UnifiedRequest` 增加 `Tools []Tool`（`json:"tools,omitempty"`）和 `ToolChoice any`（`json:"tool_choice,omitempty"`，用 `any` 支持 `"auto"`/`"none"`/`{"type":"function","function":{"name":...}}` 三种形态）。

**`internal/adapter/openai/adapter.go`** — `wireRequest` 增加同名字段，`BuildRequest` 中 `wr.Tools = req.Tools; wr.ToolChoice = req.ToolChoice`（OpenAI 兼容上游原生支持该格式）。

### 2. 响应侧（流式）：透传 delta.tool_calls

OpenAI 流式规范：首个 tool_call chunk 带 `index/id/type/function.name`，后续同 `index` 的 chunk 只带 `function.arguments` 的增量字符串。需保留 `index` 以便客户端重组。

**`internal/adapter/adapter.go`** — `Chunk` 增加：
```go
DeltaToolCalls []ToolCallDelta `json:"-"`
```
新增 `ToolCallDelta{Index int; ID, Type string; Function FunctionCallDelta}`，`FunctionCallDelta{Name, Arguments string}`（与现有 `FunctionCall` 同形，但语义为增量——arguments 可为部分 JSON）。

**`internal/adapter/openai/adapter.go`**：
- `wireStreamChunk.Choices[].Delta` 增加 `ToolCalls []<内联>`，内联结构含 `Index int` + `ID/Type` + `Function{Name,Arguments}`。
- `Recv()` 中将解析到的 tool_calls 拷入 `c.DeltaToolCalls`。

**`internal/proxy/stream.go`**：
- `wireStreamDelta` 增加 `ToolCalls []<内联>`（同 OpenAI 流式形状，含 `index`）。
- `toWireChunk`：当 `c.DeltaToolCalls` 非空时填入 delta.tool_calls；同时把"有 delta tool calls"纳入"是否生成 choice"的判断条件（当前只看 `DeltaRole/DeltaContent/FinishReason`，否则 tool_calls chunk 会被当 usage-only 丢掉 choice）。

### 3. 测试

- **请求侧**：`internal/adapter/openai/adapter_test.go` 新增 `TestBuildRequest_ForwardsTools`，断言 `tools` 与 `tool_choice:"auto"` 出现在 body。
- **流式响应侧**：新增 testdata `testdata/chat_stream_tools.txt`（含 `delta.tool_calls` 的首/续 chunk），新增 `TestParseStream_ToolCalls` 断言重组出正确的 tool_call 序列（index/id/name/arguments 拼接）。
- **proxy 透传**：`internal/proxy/stream_test.go` 新增 `TestToWireChunk_ToolCalls`，断言 `adapter.Chunk{DeltaToolCalls:...}` 经 `toWireChunk` 后 delta 含 `tool_calls` 数组及 `index`。
- 新增 testdata：`internal/adapter/openai/testdata/chat_stream_tools.txt`（手工构造 OpenAI 流式 tool_calls 形状的 SSE）。

### 4. 回归与文档

- 跑 `go test ./internal/...` 全量回归。
- 不改 Claude 适配器（已知不支持 OpenAI tool 格式，commit e4f484e 已声明）。
- 不动日志层（按设计不记请求/响应正文；本次纯适配层修复）。

## 不做的事
- 不实现 DSML 文本格式解析（根因是 tools 没送达，修了 tools 透传后上游会走结构化通道，不再产出 DSML 文本）。
- 不改 Claude 适配器。
- 不改计费/日志/路由。

## 验证
- 单测全绿。
- 部署后用 code agent 经 gateway 跑一次工具调用，确认 agent 正常执行 Bash 等工具、不再出现 DSML 文本。