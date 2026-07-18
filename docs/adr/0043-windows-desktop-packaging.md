# ADR-0043: Desktop Windows 打包 — Wails v2 NSIS + CI Windows runner

- Status: Accepted
- Date: 2026-07-19
- Builds on ADR-0041 (desktop gateway orthogonality), ADR-0042 (Windows dev env = WSL2)
- Supersedes: ADR-0042 §3「CI 平台仅 Linux」+ §39「Windows 桌面客户端打包需重新评估」——**仅针对 desktop 打包这一窄面**；ADR-0042 其余条款（开发者官方环境 = WSL2、脚本 POSIX-only、`make ci` 仅 Linux）继续有效
- SoT: `design/desktop.md` §10.1（桌面 UI 技术选型与打包）、`deploy/desktop/`（打包层实现）

## Context

ADR-0041 落地时 desktop 仅 macOS 打包（`scripts/build-desktop.sh` 硬编码 `-platform darwin/universal`），ADR-0042 §39 明确「未来若需支持 Windows 桌面客户端打包，需重新评估本决策并起新 ADR supersede」。

现状（2026-07-19）：

- Wails v2 已落地 macOS：`deploy/desktop/wails.json` + `deploy/desktop/app/{assets,desktop}.go` + `deploy/desktop/build/darwin/`。
- SQLite 用纯 Go 驱动 `glebarez/sqlite`（基于 `modernc.org/sqlite`），**Windows 交叉编译无 CGO 障碍**。
- 前端是纯 SPA（`desktop-ui/`，Vite + React 19），平台无关。
- 唯一 CGO 依赖：Wails 的 WebView2 绑定（Windows）——必须在 Windows（或带 mingw 的环境）上 `wails build`，无法从 macOS/Linux 直接交叉编译。
- `go.mod` 已有 `github.com/wailsapp/go-webview2 v1.0.22`（indirect），Wails 官方支持 Windows 一等公民。
- `deploy/desktop/app/desktop.go` 的 `openConfigFolder()` 硬编码 macOS `open -R`，需跨平台化（已修）。
- CI（`.github/workflows/ci.yml`）仅 `ubuntu-latest`，无 Windows runner。

## Decision

### 1. 继续用 Wails v2，不换栈

desktop macOS 路径已跑通，Windows 是 Wails 官方一等公民（WebView2）。换 Electron / Tauri 等于推翻 `deploy/desktop/app/` 的 Wails 应用层（`OnStartup` / `AssetServer.Handler` 反向代理、原生菜单、生命周期绑定），违反「不涉及功能修改」原则。

### 2. Windows 安装包格式 = NSIS .exe

Wails 默认格式，单文件 installer，体积小，支持静默安装（`/S`）。不选 MSI（需 wix，复杂度高，企业 GPO 场景目前未提出需求）；不选 Portable .zip（无安装器、无快捷方式、无注册表项，体验差）。

### 3. 目标架构 = 仅 amd64

覆盖所有 64 位 Windows，最常见配置，构建最快。ARM64（Surface Pro X 等）暂不支持，后续按需加 `-platform windows/arm64`。

### 4. CI 加 `desktop-windows-build` job（windows-latest）

- 触发条件：`push` 到 `main`（与 `ci-heavy` 一致），避免每个 PR 烧 Windows 分钟。
- 步骤：装 Go + Node + Wails CLI + NSIS → `./scripts/build-desktop.sh windows` → 上传 `.exe` artifact。
- **不跑 `make ci`**——ADR-0042 §3 的「CI 平台仅 Linux」对 `make ci` 仍然成立；Windows runner 只跑 desktop 打包这一窄面。
- 用 `shell: bash`（Windows runner 默认 Git-Bash）跑 POSIX 脚本，符合 ADR-0042 §2「新增脚本只需 Linux/macOS/WSL2 可运行」的精神延伸（CI 上由项目方保证 bash 可用）。

### 5. 开发者本���构建 Windows 包

### 5. 开发者本机构建 Windows 包（两条路径）

**路径 A（推荐）：WSL2 / Linux 交叉编译**

```bash
sudo apt install -y mingw-w64 nsis   # 一次性安装工具链
make desktop-build-windows-cross     # 跑交叉编译
```

底层执行 `CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 wails build -platform windows/amd64 -nsis`。Wails 官方支持 Linux/macOS 交叉编译 Windows，mingw-w64 提供 CGO 交叉编译器（WebView2 绑定需要），NSIS 在 Linux 上原生可用（`makensis` 跨平台）。WSL2 = Linux 用户态，与原生 Ubuntu 同。

**路径 B：Windows 原生**

在 Windows 上装 Go 1.22+ + Node 18+ + NSIS ≥3.9 + Wails CLI，跑 `make desktop-build-windows`。

两条路径产出物一致（同一个 `.exe`）。开发者按已有环境选择：日常用 WSL2 的走路径 A（无需切到 Windows），在 Windows 原生开发的走路径 B。

## Consequences

正面：

- desktop 在 Windows 上有官方安装包，覆盖个人开发者主流平台。
- 复用现有 Wails 应用层 100%，零功能改动。
- macOS 与 Windows 打包脚本统一在 `scripts/build-desktop.sh`（参数化 TARGET），维护成本最低。
- CI 自动产出 `.exe` artifact，为后续 release 流程铺路。

负面 / 约束：

- CI 多一个 Windows job（每月数十分钟成本，push-to-main 触发）。
- Windows runner 与 Linux runner 行为差异（路径分隔符、shell）需在 `build-desktop.sh` 内消化；脚本已用 `filepath.FromSlash` 处理 `explorer.exe /select,` 调用。
- 无代码签名（需 EV 证书，企业级议题）——Windows SmartScreen 会首次拦截，用户需手动「仍要运行」。留待后续 ADR。
- ARM64 Windows 暂不支持。

## Alternatives considered

- **MSI（WiX）**：企业 GPO/SCCM 批量部署友好，但 Wails 需额外配置 WiX 工具链，复杂度高；当前无企业部署需求。后续若提出可起新 ADR。
- **Portable .zip**：解压即用，最轻量但无快捷方式 / 注册表项 / 卸载器，体验差，不分发。
- **Electron**：推翻现有 Wails 应用层（`deploy/desktop/app/`），引入 Node.js 运行时（~150MB），违反「不涉及功能修改」。
- **Tauri**：同 Electron，推翻现有应用层；Rust 工具链引入成本。
- **WSL2 内交叉编译（已采纳为路径 A）**：原判断「工具链复杂」经核实有误——只需 `apt install mingw-w64 nsis` 两个包，不需要 Windows SDK headers（mingw-w64 自带）。Wails 官方文档与社区镜像（`ghcr.io/rocketblend/cross-wails`）均支持此路径。已提升为开发者推荐路径。

## 参考

- 现状盘点：`deploy/desktop/wails.json`、`scripts/build-desktop.sh`、`deploy/desktop/app/desktop.go`
- 相关 ADR：ADR-0041（desktop gateway 正交性）、ADR-0042（Windows 开发环境 = WSL2）
- 设计文档：`design/desktop.md` §10.1（桌面 UI 技术选型）、§14（MVP 分期）
- Wails Windows 打包官方文档：https://wails.io/docs/guides/windows-installer/
