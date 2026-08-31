# voxeltoad 桌面网关 — 安装与使用指南

面向个人开发者的本地 LLM 网关：把 CodeBuddy / Codex / Claude Code / 各类脚本
统一接到一个本地入口，稳定路由与故障转移，并看清每一次调用的成功率、延迟、
Token 用量与完整 Trace。全部数据留在本机，无需任何云服务。

## 1. 系统要求

- macOS 12+（Apple Silicon 与 Intel 均可，.dmg 为 universal 双架构）
- 约 100 MB 磁盘空间（应用本体；观测数据随使用量增长，默认保留 30 天）

Windows 版提供 NSIS 安装包（`.exe`），构建方式见
[deploy/desktop/README.md](../../deploy/desktop/README.md)。

## 2. 安装

1. 获取 `voxeltoad-desktop.dmg`，双击挂载。
2. 将 **voxeltoad 桌面** 拖入 `Applications` 文件夹。
3. 首次打开：因当前分发包使用 **ad-hoc 签名**（无 Apple 开发者账号），
   Gatekeeper 会提示「无法验证开发者」。在访达中对应用**右键 → 打开 →
   再点「打开」**即可，此后正常双击启动。

## 3. 首次配置（四步向导）

首次启动后，概览页顶部会出现配置向导，按唯一的高亮下一步走完四步：

1. **Provider（供应商）**——添加一个上游，例如 DeepSeek / Kimi / GLM /
   TokenHub：填写名称、类型、上游 `base_url`（供应商开放平台文档可查）与
   **上游 API Key**。Key 以明文 `plain://` 存在本机 YAML，只在本机使用。
2. **Model（模型）**——定义本地别名并挂到上游供应商，例如
   `deepseek-v4-flash` → 深度求索的 `deepseek-v4-flash`。可填价格用于成本估算。
3. **Route（路由）**——为该模型别名选择路由策略
   （priority / weighted / round_robin / session_affinity）与候选供应商。
4. **Test（连通性测试）**——到「Playground」页选刚建的模型发一个小请求，
   确认整条链路可用。

三步配置完成后向导卡片自动隐藏，概览退化为常规运行态统计。

## 4. 获取网关地址与 API Key

- **网关地址**：默认 `http://127.0.0.1:8787/v1`（可在 设置 → 网关监听 修改端口）。
- **API Key**：菜单栏「复制 API key」，或 设置 → API 密钥 查看。默认 key 为
  `desktop-local-default-key`（也可用环境变量 `GATEWAY_DESKTOP_KEY` 指定）。

> 建议安装后立刻在 设置 → API 密钥 点「轮换」，换成随机 `dt-sk-*` 密钥。
> 轮换后旧 key 立即失效，**所有 Agent 都要更新**；新明文只在轮换时展示一次。

## 5. 配置你的 Agent

任何支持自定义 OpenAI 兼容端点的工具都能接入：

| 配置项 | 值 |
|---|---|
| Base URL | `http://127.0.0.1:8787/v1` |
| API Key | 你的网关 key（见上节） |
| Model | 你在向导中定义的模型别名 |

- **CodeBuddy / Codex / Claude Code / opencode 等**：在各自的模型或供应商
  设置中填入上表三项。Claude 协议的客户端也可直接使用
  `http://127.0.0.1:8787`（网关同时兼容 OpenAI `/v1/chat/completions` 与
  Anthropic `/v1/messages` 入站协议）。
- **脚本 / SDK**：与 OpenAI SDK 的 `base_url` 用法一致，例如
  `OpenAI(base_url="http://127.0.0.1:8787/v1", api_key="<网关key>")`。

多个调用源共用同一个网关时，可在请求头里带 `X-Voxeltoad-Session: <名字>`
显式区分会话，便于在会话浏览器里分开查看。

## 6. 日常使用

- **概览**：总调用、成功率、平均延迟 / TTFT、Token、供应商 / 模型 / Agent
  分布、错误类型分布与估算成本。
- **请求日志**：多维过滤 + 分页；任意一行可展开**分发路径**（评估了哪些候选、
  跳过 / 调用了谁、为何切换、最终谁响应）。
- **会话 / Trace**：按会话浏览请求时间线，点开看完整 messages、请求与响应
  原文、错误原文；重要会话可**收藏置顶**并豁免自动清理。
- **Prompt 收藏**：从任意 Trace 的 messages 一键收藏好 prompt，集中管理。
- **供应商**：每个端点的熔断状态、成功率与最后观测时间（被动健康，不产生
  探测请求）。
- **Playground**：随时对任意模型发小请求验证链路。
- **运行日志**：查看进程日志排查问题，完整历史在 `~/.voxeltoad/logs/desktop.log`。

## 7. 数据与隐私

全部数据只存在本机 `~/.voxeltoad/`：

| 文件 | 内容 |
|---|---|
| `desktop.db` | SQLite：请求日志、分发路径、Trace、收藏、API key 哈希 |
| `desktop.yaml` | 配置（含上游供应商 key，`plain://` 明文） |
| `logs/desktop.log` | 运行日志（10 MB 轮转） |

- Trace 正文采集**默认开启**（便于排障），可在 设置 中关闭或限制单条上限。
- 默认保留 30 天；设置页支持**按时间立即清理**与**清空全部观测数据**；
  被收藏的会话不会被自动清理或误清（清空全部时单独确认）。

## 8. 常见问题

- **启动报端口占用**：8787 被其他进程占用时，应用会弹窗提示——改
  设置 → 网关监听 端口，或在配置 YAML 中调整 `gateway.addr` 后重载。
- **轮换 key 后 Agent 全部 401**：轮换会使旧 key 立即失效，用菜单
  「复制 API key」把新 key 更新到各 Agent 配置。
- **重启后提示 key 明文不可知**：这是设计行为——轮换过的 key 只存哈希，
  重启后无法找回明文；到 设置 → API 密钥 再轮换一次即可获得新明文。
- **供应商 401/403**：检查 Provider 配置里的上游 API Key 与 `base_url`
  （对照供应商开放平台文档），到 Playground 用小请求验证。
- **卸载**：删除应用 + `rm -rf ~/.voxeltoad`（会清掉所有本地观测数据，
  收藏与配置也会一并删除，请先备份需要的 Trace / Prompt）。

## 9. 反馈与更多文档

- 架构与设计决策：[design/desktop.md](../../design/desktop.md)、[docs/adr/](../adr/)
- 故障转移排查：[docs/ops/failover-troubleshooting.md](../ops/failover-troubleshooting.md)
