#!/usr/bin/env bash
# Build the gateway and admin binaries into ./bin, stamping version metadata.
#
# Usage:
#   scripts/build.sh                 # build for the host platform
#   GOOS=linux GOARCH=amd64 scripts/build.sh   # cross-compile
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LDFLAGS="-s -w \
  -X main.version=${VERSION} \
  -X main.commit=${COMMIT} \
  -X main.date=${DATE}"

mkdir -p bin

echo "Building gateway (version=${VERSION} commit=${COMMIT})"
CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o bin/gateway ./cmd/gateway

echo "Building admin"
CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o bin/admin ./cmd/admin

echo "Done. Binaries in ./bin:"
ls -lh bin
