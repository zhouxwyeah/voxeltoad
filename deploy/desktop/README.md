# Desktop Personal Gateway — macOS App Packaging

This directory is the Wails v2 packaging layer that turns the desktop personal
gateway (`cmd/desktop`, `internal/desktopstore`, `internal/desktopapi`) into a
standard macOS `.app` bundle. See `design/desktop.md` §10 and ADR-0041.

## What lives here

- `wails.json` — Wails project config. `frontend:dir` points at the repo-root
  `desktop-ui/` (top-level, parallel to `web/`); `assetdir` is its `dist/`.
- `app/assets.go` — `//go:embed all:dist` + `var Assets embed.FS`. The `dist/`
  subdir is populated by `scripts/build-desktop.sh` from `desktop-ui/dist/`.
  Not guarded by a build tag so Linux CI `go build ./...` resolves the Wails
  dependency (cgo linkage only happens at final `wails build` link time).
- `app/desktop.go` — Wails app context: HTTP server lifecycle (`OnStartup`/
  `OnShutdown`), native menu (copy API key / reload config / open config
  folder), hide-to-tray behavior.
- `build/darwin/Info.plist` — bundle manifest. `CFBundleIdentifier` is
  `dev.voxeltoad.desktop`; declares `NSLocalNetworkUsageDescription`
  (the gateway binds 127.0.0.1 — macOS Sonoma+ prompts even for loopback).
- `build/darwin/dev.entitlements.plist` — dev-build entitlements (App Sandbox
  OFF; needs network server/client + user-selected files for the SQLite DB /
  YAML config paths the user chooses).
- `build/appicon.png` — 1024×1024 master icon. `wails build` generates the
  platform-specific `icon.icns` from this.

## Architecture notes

- **The data plane stays on raw `net/http`** (`cmd/desktop/main.go`): the
  reused `proxy.Router` serves `text/event-stream` for LLM streaming
  (`WriteTimeout: 0`, `http.Flusher` per chunk). Third-party Agents
  (CodeBuddy/Codex/Claude Code) hit `http://127.0.0.1:<port>/v1` as their
  `base_url` — this MUST stay a plain HTTP server, NOT the Wails AssetServer
  (no SSE support, unreachable from external processes).
- **The Wails window loads the SPA from the embedded `app/dist/`** via the
  AssetServer. The SPA then `fetch`es `/api/v1/*` (read API + config CRUD)
  and Agents POST `/v1/*` (data plane) — both on the same origin
  (`http://127.0.0.1:<port>`), so no CORS.
- **Wails bindings** are reserved for native-shell-only actions: the menu bar
  (copy API key, reload config, open config folder, quit) drives Go methods
  directly. The read API + config CRUD stay HTTP so they're testable
  headlessly (`internal/desktopapi/*_test.go`).
- **`CGO_ENABLED=1`** is required (Wails binds Cocoa/WebKit). `glebarez/sqlite`
  is pure Go and coexists fine.

## Building

Requires Go 1.22+, Xcode Command Line Tools, and the Wails CLI:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make desktop-build
```

Output: `deploy/desktop/build/bin/voxeltoad-desktop.app`.

The build script (`scripts/build-desktop.sh`) does in order:
1. `cd desktop-ui && npm ci && npm run build` → `desktop-ui/dist/`
2. `cp -R desktop-ui/dist deploy/desktop/app/dist` (the embed source)
3. `cd deploy/desktop && wails build` → reads `wails.json`, compiles the
   `package main` in this directory (`main.go` imports `internal/desktopapp`),
   embeds `app/dist/`, produces `.app`

## Signing & notarization (distribution)

For a distributable build (vs. local dev):

1. Get a Developer ID Application certificate from Apple Developer Program.
2. Replace `dev.entitlements.plist` usage with a hardened-runtime entitlements
   set in `wails.json` (`darwin/signandnotarise` section).
3. `wails build -platform darwin/universal -sign <identity> -notarize`.

Local dev builds (just `make desktop-build`) are unsigned and will trigger
Gatekeeper on first launch — right-click → Open to bypass.

## Reuse vs the enterprise gateway

The desktop gateway reuses the enterprise data plane verbatim
(`internal/proxy` / `adapter` / `plugin` / `observability` / `auth` /
`config/schema.go`) and replaces only the persistence + config source:
- `internal/desktopstore` (L2, peer of `internal/store`) — SQLite
- `internal/desktopapi` (L3, peer of `internal/admin`) — read API + config CRUD
- `cmd/desktop` (L4 composition root) — assembles the above + Wails window

See `design/architecture.md` §三入口依赖矩阵 and ADR-0041 for the
orthogonality argument.
