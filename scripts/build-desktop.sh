#!/usr/bin/env bash
# Build the desktop personal gateway as a native installer.
#
# Three stages:
#   1. Build the SPA (desktop-ui/) → desktop-ui/dist/
#   2. Sync dist/ → deploy/desktop/app/dist/ (the //go:embed source)
#   3. `wails build` under deploy/desktop/ → bundles the platform installer
#
# Platform selection:
#   ./scripts/build-desktop.sh                  # default: darwin
#   ./scripts/build-desktop.sh darwin           # macOS .app bundle (universal)
#   ./scripts/build-desktop.sh windows          # Windows NSIS .exe (run ON Windows)
#   ./scripts/build-desktop.sh windows-cross    # Cross-compile Windows .exe from Linux/WSL2
#   TARGET=windows-cross ./scripts/build-desktop.sh
#
# Requires: Go 1.22+, Node 18+, and the Wails CLI
# (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).
# CGO_ENABLED=1 is implicit (Wails binds Cocoa/WebKit on macOS, WebView2 on
# Windows). For Windows builds, NSIS ≥3.9 must be on PATH:
#   - Windows:  choco install nsis
#   - Linux/WSL2: sudo apt install mingw-w64 nsis
#
# Output:
#   darwin:         deploy/desktop/build/bin/voxeltoad-desktop.app
#   windows*:       deploy/desktop/build/bin/voxeltoad-desktop-amd64-installer.exe
set -euo pipefail

TARGET="${1:-${TARGET:-darwin}}"
case "$TARGET" in
  darwin|macos)       TARGET=darwin ;;
  windows|win)        TARGET=windows ;;
  windows-cross|cross) TARGET=windows-cross ;;
  *) echo "error: unknown TARGET '$TARGET' (expected: darwin|windows|windows-cross)"; exit 2 ;;
esac

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
echo "== stage 3: wails build ($TARGET, CGO_ENABLED=1) =="
# wails build reads deploy/desktop/wails.json. The project directory is
# deploy/desktop/, which now contains a thin main.go that imports
# internal/desktopapp. The embed.FS lives in deploy/desktop/app/assets.go
# and points at app/dist/. We've pre-synced dist/ so the embed resolves at
# compile time — wails build itself doesn't re-run npm.
cd deploy/desktop

case "$TARGET" in
  darwin)
    CGO_ENABLED=1 "$WAILS_BIN" build -tags desktop -platform darwin/universal
    ;;
  windows)
    # Run on Windows. NSIS must be installed (choco install nsis).
    # Wails auto-invokes makensis using the templates under build/windows/
    # (or its bundled defaults if those files are absent).
    CGO_ENABLED=1 "$WAILS_BIN" build -tags desktop -platform windows/amd64 -nsis
    ;;
  windows-cross)
    # Cross-compile from Linux/WSL2. Requires mingw-w64 + nsis:
    #   sudo apt install -y mingw-w64 nsis
    # Wails honors CC/CXX for the CGO step (WebView2 bindings).
    if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
      echo "error: x86_64-w64-mingw32-gcc not found."
      echo "       install with: sudo apt install -y mingw-w64"
      exit 1
    fi
    if ! command -v makensis >/dev/null 2>&1; then
      echo "error: makensis not found."
      echo "       install with: sudo apt install -y nsis"
      exit 1
    fi
    CC=x86_64-w64-mingw32-gcc \
    CXX=x86_64-w64-mingw32-g++ \
    CGO_ENABLED=1 \
    GOOS=windows \
    GOARCH=amd64 \
      "$WAILS_BIN" build -tags desktop -platform windows/amd64 -nsis
    ;;
esac

echo
case "$TARGET" in
  darwin)
    APP="$ROOT/deploy/desktop/build/bin/voxeltoad-desktop.app"
    if [ -d "$APP" ]; then
      echo "✓ built: $APP"
      echo "  size: $(du -sh "$APP" | awk '{print $1}')"
      echo "  open with: open '$APP'"
    else
      echo "✗ expected output not found at $APP"
      exit 1
    fi
    ;;
  windows|windows-cross)
    # Wails names the installer <wails.json:name>-amd64-installer.exe.
    # Currently `name=voxeltoad-desktop`, so the output is
    # voxeltoad-desktop-amd64-installer.exe.
    EXE="$ROOT/deploy/desktop/build/bin/voxeltoad-desktop-amd64-installer.exe"
    if [ -f "$EXE" ]; then
      echo "✓ built: $EXE"
      echo "  size: $(du -sh "$EXE" | awk '{print $1}')"
    else
      echo "✗ expected output not found at $EXE"
      exit 1
    fi
    ;;
esac
