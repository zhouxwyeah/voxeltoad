# Unit Testing Guide

> 测试框架：Go 标准 `testing` + 表驱动测试（table-driven）。断言可选 `testify/assert`。

## Running Tests

```bash
go test ./...                          # 全部
go test ./internal/adapter/...         # 某个包
go test -run TestExtractUsage ./...    # 指定用例
go test -race ./...                    # 竞态检测（流式/并发必跑）
go test -cover ./...                   # 覆盖率
make ci                                # 全套门禁：fmt/vet/lint/arch-check/test + check-errors + devstack 烟测 + SDK
make ci-web                            # 前端门禁：check-i18n + typecheck + lint + unit
```

### Catalog 校验（CI 门禁一部分）

- `make check-errors` — 扫描 `internal/apperr/*.go` 的 `apperr.New()` 调用，校验错误
  码唯一、snake_case、HTTP status 合法、i18n key 在 `web/src/locales/en/errors/<domain>.json`
  中存在。新增错误码后必跑。
- `make check-i18n` — 递归对比 `en/*.json` 与 `zh/*.json` 的扁平 key 集合，缺失即 fail。
  新增 locale 文件后必跑。

## What to Test

测**逻辑**，不测**接线**。好的单测覆盖那些会以不明显的方式出错的代码。

> 如果一个测试只能证明「代码做了代码所做的事」，它没有价值。

**必测：**

- **协议适配转换** —— OpenAI 兼容请求 ↔ 各供应商原生请求/响应的双向映射，尤其是字段差异、默认值、边界。
- **SSE 流式解析** —— 把上游 SSE chunk 解析/聚合为统一 chunk 模型，含半包/粘包、`[DONE]` 终止、错误事件。
- **Token 计费** —— usage 提取、定价计算、配额扣减的边界（0、null usage、超额）。
- **限流算法** —— 令牌桶/漏桶的时间窗口、并发正确性（配合 `-race`）。
- **路由/故障切换决策** —— 多供应商权重、健康状态变化下的选择逻辑。
- **敏感词/规则表达式** —— 命中与不命中的分支。

**跳过：**

- 类型定义、常量、注册表登记这类接线代码。
- 只是转调第三方库的薄封装（如直接包一层 gorm/redis client）。
- HTTP handler 的纯路由绑定（这类交给集成测试覆盖）。
- 配置结构体的字段定义。

写测试是一次 review。断言**正确行为**，而非**当前行为**。若实现有 bug，改实现，别让测试迁就 bug。

测试难度阶梯：**纯函数 < 内部逻辑 < 需要 mock 外部依赖的逻辑**。当 proxy/handler 里逻辑变复杂时，先把它抽成 `pkg/` 或 `internal/<pkg>/lib` 的纯函数再测。

## File Placement

测试文件与源码同包共置（Go 惯例）：

```
internal/adapter/openai/adapter.go
internal/adapter/openai/adapter_test.go
internal/billing/usage.go
internal/billing/usage_test.go
pkg/sse/parser.go
pkg/sse/parser_test.go
```

跨包的黑盒测试用 `package xxx_test` 后缀包名；需要测包内未导出函数时用同包名。

## Testing Patterns

### 表驱动测试（默认范式）

```go
func TestExtractUsage(t *testing.T) {
    tests := []struct {
        name    string
        resp    *UnifiedResponse
        want    *Usage
        wantErr bool
    }{
        {"标准 usage", &UnifiedResponse{Usage: &Usage{PromptTokens: 10, CompletionTokens: 20}}, &Usage{10, 20, 30}, false},
        {"缺失 usage 字段", &UnifiedResponse{}, nil, true},
        {"流式聚合后的 usage", streamAggregated(), &Usage{5, 15, 20}, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ExtractUsage(tt.resp)
            if (err != nil) != tt.wantErr {
                t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("got %+v, want %+v", got, tt.want)
            }
        })
    }
}
```

### 协议适配测试

对每个 Adapter，用「输入统一请求 → 断言生成的上游 http.Request」与「输入上游响应样本 → 断言解析出的统一响应」两个方向测试。把真实供应商的响应样本存为 `testdata/<provider>_response.json`、`testdata/<provider>_stream.txt`，避免在代码里硬编码大段 JSON。

```go
func TestClaudeAdapter_ParseStream(t *testing.T) {
    raw := readTestdata(t, "claude_stream.txt") // 真实 SSE 样本
    sr, _ := adapter.ParseStream(mockResp(raw))
    var chunks []Chunk
    for { c, err := sr.Recv(); if err == io.EOF { break }; chunks = append(chunks, c) }
    // 断言 chunk 数量、首 chunk 角色、末 chunk usage 聚合正确
}
```

### Mock 外部依赖

- **接口先行** → 测试里注入 fake 实现，不 mock 具体 struct。
- **PG**：配额、持久化这类逻辑优先抽成纯算法测；确需真实库的，本地/单测用 `fergusstrange/embedded-postgres`（进程内拉起真实 PostgreSQL，免 Docker），CI 集成测试用 testcontainers（见 design/e2e.md）。**不 mock SQL，不用 SQLite 替代**（方言/JSONB 差异会埋坑）。详见下方「embedded-postgres 设置」。
- **限流/缓存**：默认是内存实现，直接用注入时钟做表驱动测试（见 `internal/plugin/ratelimit`、`internal/plugin/cache`）。Redis 实现属多实例扩展项，落地时再针对其后端单独测。
- **HTTP 上游**：用 `httptest.Server` 模拟供应商，断言发出的请求 + 喂入构造的响应。

### embedded-postgres 设置

需要真实库的 `internal/store` 仓储测试用 `fergusstrange/embedded-postgres`：它在测试进程内
下载并启动一个真实 PostgreSQL 二进制，免 Docker，与生产同方言（含 JSONB）。

依赖：

```bash
go get github.com/fergusstrange/embedded-postgres
```

**约定**：每个需要库的测试包用 `TestMain` 启动一个共享实例（按包复用，避免每个用例重复启停
~1–2s），并通过 `t.Cleanup` / 事务回滚做用例间隔离。把这部分封装到 `internal/store` 的测试
辅助里，仓储测试直接取连接。

```go
//go:build dbtest

package store_test

import (
    "fmt"
    "os"
    "testing"

    embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

var testDSN string

func TestMain(m *testing.M) {
    pg := embeddedpostgres.NewDatabase(
        embeddedpostgres.DefaultConfig().
            Port(54329).             // 避开默认 5432，防与本地 PG 冲突
            Database("voxeltoad_test"),
    )
    if err := pg.Start(); err != nil {
        fmt.Fprintln(os.Stderr, "embedded-postgres start:", err)
        os.Exit(1)
    }
    testDSN = "postgres://postgres:postgres@localhost:54329/voxeltoad_test?sslmode=disable"

    code := m.Run()

    _ = pg.Stop() // 始终在退出前停库
    os.Exit(code)
}
```

要点：

- **构建标签隔离**：用 `//go:build dbtest`（或归入 e2e 标签）把需要库的测试与默认 `make test`
  分开，保证 `go test ./...` 在无网络/无库环境下依然快速、纯净。运行：`go test -tags=dbtest ./internal/store/...`。
- **首次运行会联网**下载 PG 二进制并缓存，CI 需放行或预热缓存。
- **用例隔离**：每个用例在独立事务中跑、结束 `ROLLBACK`，或建独立 schema；不要依赖跨用例的残留数据。
- **迁移**：测试启动后对 `testDSN` 跑与生产相同的 schema 迁移，确保结构一致。
- **端口**：固定一个非默认端口，避免与 `make dev-deps` 起的本地 PG 撞端口。

> 区分：`make dev-deps`（Docker PG）用于**运行整个网关服务**；embedded-postgres 用于**跑测试**。
> 二者都是真实 PostgreSQL，互不冲突（见 README）。

### 并发与流式

涉及流式转发、限流计数、配置热更新（`atomic.Value`）的测试，**必须加 `-race`**。CI 默认带 `-race` 跑。

## Architecture Alignment

单测遵守与源码相同的依赖规则（见 [architecture.md](architecture.md)）：

- `pkg/` 的测试只依赖标准库与该包自身 —— 不 import 任何 `internal/`。
- `internal/adapter/<x>` 的测试不 import 其它 adapter；`internal/plugin/<x>` 同理。
- 数据面（proxy）测试不 import 管理面（admin），反之亦然。
