#!/usr/bin/env bash
# Build the desktop personal gateway as a macOS .app bundle.
#
# Three stages:
#   1. Build the SPA (desktop-ui/) → desktop-ui/dist/
#   2. Sync dist/ → deploy/desktop/app/dist/ (the //go:embed source)
#   3. `wails build` under deploy/desktop/ → bundles the .app
#
# Requires: Go 1.22+, Xcode Command Line Tools, Node 18+, and the Wails CLI
# (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`). CGO_ENABLED=1
# is implicit (Wails binds Cocoa/WebKit).
#
# Output: deploy/desktop/build/bin/desktop-gateway.app
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WAILS_BIN="${WAILS_BIN:-wails}"  # override if not on PATH (e.g. /Users/.../go/bin/wails)

cd "$ROOT"

if ! command -v "$WAILS_BIN" >/dev/null 2>&1; then
  GOPATH_BIN="$(go env GOPATH)/bin/wails"
  if [ -x "$GOPATH_BIN" ]; then
    WAILS_BIN="$GOPATH_BIN"
  else
    echo "error: wails CLI not found on PATH or in \$(go env GOPATH)/bin."
    echo "       install with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
  fi
fi

echo "== stage 1: build SPA (desktop-ui) =="
( cd desktop-ui && npm ci && npm run build )

echo
echo "== stage 2: sync dist → deploy/desktop/app/dist =="
rm -rf deploy/desktop/app/dist
cp -R desktop-ui/dist deploy/desktop/app/dist
echo "  synced $(find deploy/desktop/app/dist -type f | wc -l | tr -d ' ') files"

echo
echo "== stage 3: wails build (macOS, CGO_ENABLED=1) =="
# wails build reads deploy/desktop/wails.json. The project directory is
# deploy/desktop/, which now contains a thin main.go that imports
# internal/desktopapp. The embed.FS lives in deploy/desktop/app/assets.go
# and points at app/dist/. We've pre-synced dist/ so the embed resolves at
# compile time — wails build itself doesn't re-run npm.
cd deploy/desktop
CGO_ENABLED=1 "$WAILS_BIN" build -tags desktop -platform darwin/universal

echo
APP="$ROOT/deploy/desktop/build/bin/voxeltoad-desktop.app"
if [ -d "$APP" ]; then
  echo "✓ built: $APP"
  echo "  size: $(du -sh "$APP" | awk '{print $1}')"
  echo "  open with: open '$APP'"
else
  echo "✗ expected output not found at $APP"
  exit 1
fi
