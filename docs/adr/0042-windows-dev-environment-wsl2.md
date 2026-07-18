# ADR-0042: Windows 开发者官方环境 = WSL2（脚本保持 POSIX-only，CI 仅 Linux）

- Status: Accepted
- Date: 2026-07-17

## Context

项目需要接纳 Windows 平台开发者参与代码与文档写作。现状（2026-07-17 盘点）：

- 仓库 18 个 `scripts/*.sh` 中 16 个是 POSIX-only，依赖 bash + setsid / lsof / pgrep / jq / openssl / mktemp / 进程组负 PID 等。
- PR 门禁 `make ci` 调用 5+ 个上述 bash 脚本（`devstack-test` / `arch-check` / `check-errors` / `check-permissions` / `check-docs` / `sdk-chat-e2e` / `adminstack-test`）。
- CI（`.github/workflows/ci.yml`）跑 `ubuntu-latest`，无 Windows runner。
- 前端 / SDK / desktop-ui 的 npm scripts 已跨平台；Go 代码层无 `_unix.go` / `_windows.go`，路径处理统一 `filepath.Join`，仅 5 处 SIGTERM 在 Windows 下优雅停机语义失效（不阻塞本次目标）。
- 文档写作链路唯一自动化是 `scripts/check-docs.sh`（POSIX-only）。

Windows 开发者可选的本地 bash 环境：Git-Bash（MSYS2 裁剪版，缺 setsid / lsof / pgrep，coreutils 不全）、WSL2（完整 Linux 用户态，与 CI 同平台）、Cygwin / MSYS2 全量（重、非主流）。

## Decision

1. **Windows 开发者的官方开发环境 = Windows 11 + WSL2（Ubuntu 22.04+）**。在 WSL2 内执行所有 `make` 目标，包括 `make ci`。
2. **脚本保持 POSIX-only，不改写**。允许现有 18 个 bash 脚本继续依赖 setsid / lsof / pgrep / jq / openssl 等 POSIX 工具；新增脚本同样不受"跨平台"约束，只需在 Linux / macOS / WSL2 三处可运行。
3. **CI 平台仅 Linux**（`ubuntu-latest`），不引入 Windows runner 或 OS matrix。Windows 兼容性以"开发者在 WSL2 内 `make ci` 通过"为准，不在 CI 上额外兜底。
4. **Git-Bash 与 PowerShell 原生不是官方支持环境**。开发者在这些环境下遇到的问题不构成 issue，文档会明确标注。
5. **GNU make 与其他 POSIX 依赖由开发者在 WSL2 内自行安装**（`sudo apt install build-essential jq openssl` 等），仓库不提供安装脚本。

## Consequences

正面：

- 脚本零改动，避免了把 16 个 bash 脚本改造为跨平台（Go / Node）的工作量。
- CI 保持单平台，工作流维护成本不增加。
- 文档写作链路（`scripts/check-docs.sh`）、PR 门禁（`make ci`）、本地起栈（`make adminstack` / `devstack` / `start-stack`）在 WSL2 下与 Linux 行为一致。
- WSL2 与 CI 同为 Ubuntu，本地-CI 差异最小。

负面 / 约束：

- Windows 开发者必须安装 WSL2，存在一次性环境成本。
- Git-Bash / PowerShell 原生不受官方支持，相关 issue 将被关闭并指向本文档。
- 未来若需支持 PowerShell 原生（如 Windows 桌面客户端打包），需重新评估本决策并起新 ADR supersede。
- Go 代码中 5 处 SIGTERM 在 Windows 原生下优雅停机失效的问题继续存在，但因为官方环境是 WSL2，不构成实际阻塞。

## Alternatives considered

- **Git-Bash 作为官方环境**：缺 setsid / lsof / pgrep，coreutils 不全，多数 `scripts/*.sh` 跑不动；target 覆盖率过低。
- **改写为跨平台脚本（Go 小工具 / Node `.mjs`）**：成本高、收益低；当前团队规模下维护双套心智不划算。
- **CI 加 Windows runner**：与本决策第 3 条冲突；在脚本保持 POSIX-only 的前提下 Windows runner 也跑不动 `make ci`。
- **要求所有脚本走 `bash scripts/foo.sh` 而非 `./scripts/foo.sh`**：可以缓解 shebang 问题，但不解决 POSIX 工具缺失，不采纳。

## 参考

- 现状盘点：访谈 Phase 1 报告（2026-07-17），列于会话计划文件。
- 开发者上手：README.md「Windows 开发者」一节。
- 术语：`docs/glossary.md`「Engineering environment」节（POSIX-only / WSL2 / 官方开发环境 / target 分级清单）。
