#!/usr/bin/env bash
# Data-plane SDK contract test — pairs with cmd/devstack. Boots a real
# gateway backed by embedded PostgreSQL + in-process mock upstream, then
# drives it through the TypeScript VoxeltoadGateway (OpenAI-compatible) client,
# exercising the exact chat completions contract published in design/e2e.md.
#
# Modes:
#   ./scripts/devstack-sdk-e2e.sh              # auto: starts devstack, tests, stops it
#   GATEWAY=http://host:8080 KEY=sk-... MODEL=chat \
#     MOCK_CONTROL_URL=http://host:8091 \
#     ./scripts/devstack-sdk-e2e.sh            # external: test an already-running gateway
#
# In auto mode nothing else needs to run (embedded PostgreSQL + mock upstream
# are in-process; first run downloads a PG binary, cached afterwards).
set -uo pipefail

GATEWAY="${GATEWAY:-}"
KEY="${KEY:-sk-devstack-client}"
MODEL="${MODEL:-chat}"
MOCK_CONTROL_URL="${MOCK_CONTROL_URL:-http://127.0.0.1:8091}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STACK_PID=""
STACK_BIN=""

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }

# --- lifecycle ---------------------------------------------------------------
# Run a pre-built binary (not `go run`) so $STACK_PID is the devstack process
# itself; `go run` would fork a child and swallow the signal, orphaning the
# embedded PostgreSQL. Also TERM the whole process group as belt-and-suspenders,
# and wait for graceful shutdown (devstack's defers stop PG + remove its dir).
cleanup() {
  if [ -n "$STACK_PID" ] && kill -0 "$STACK_PID" 2>/dev/null; then
    kill -TERM "-$STACK_PID" 2>/dev/null || kill -TERM "$STACK_PID" 2>/dev/null || true
    for _ in $(seq 1 100); do
      kill -0 "$STACK_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "-$STACK_PID" 2>/dev/null || kill -KILL "$STACK_PID" 2>/dev/null || true
    wait "$STACK_PID" 2>/dev/null || true
  fi
  [ -n "$STACK_BIN" ] && rm -f "$STACK_BIN" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

wait_ready() {
  for _ in $(seq 1 90); do
    if curl -sS -o /dev/null -m 1 "$GATEWAY/healthz" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

if [ -z "$GATEWAY" ]; then
  GATEWAY="http://127.0.0.1:8080"
  echo "building devstack binary..."
  STACK_BIN="$(mktemp -t devstack.XXXXXX)"
  if ! ( cd "$ROOT" && go build -tags devstack -o "$STACK_BIN" ./cmd/devstack ) 2>/tmp/devstack-build.log; then
    echo "$(red 'devstack build failed'):"; cat /tmp/devstack-build.log; exit 1
  fi

  echo "starting devstack (embedded PG + mock upstream; first run downloads PG)..."
  # Start in a fresh session/process group so cleanup can signal the whole tree.
  # GATEWAY_ALLOW_INSECURE_DEV=1: devstack intentionally runs with an open snapshot
  # channel (no admin plane, no internal token) — ADR-0007 dev escape hatch.
  if command -v setsid >/dev/null 2>&1; then
    setsid env GATEWAY_ALLOW_INSECURE_DEV=1 "$STACK_BIN" >/tmp/devstack-test.log 2>&1 &
  else
    env GATEWAY_ALLOW_INSECURE_DEV=1 "$STACK_BIN" >/tmp/devstack-test.log 2>&1 &
  fi
  STACK_PID=$!
  if ! wait_ready; then
    echo "$(red 'devstack did not become ready'). Last log lines:"
    tail -n 20 /tmp/devstack-test.log
    exit 1
  fi
else
  echo "testing external gateway at $GATEWAY"
  wait_ready || { echo "$(red "gateway at $GATEWAY not reachable")"; exit 1; }
fi

echo "gateway ready at $GATEWAY (mock-control $MOCK_CONTROL_URL)"

# Ensure SDK deps are present, then run the opt-in e2e test (vitest) that
# drives the VoxeltoadGateway client against the live gateway.
if [ ! -d "$ROOT/sdk/typescript/node_modules" ]; then
  echo "installing SDK dependencies..."
  ( cd "$ROOT/sdk/typescript" && npm install --silent ) || { echo "$(red 'npm install failed')"; exit 1; }
fi

echo "running data-plane SDK contract test (VoxeltoadGateway client)"
set +e
VOXELTOAD_E2E=1 \
VOXELTOAD_BASE_URL="$GATEWAY/v1" \
VOXELTOAD_API_KEY="$KEY" \
VOXELTOAD_MODEL="$MODEL" \
VOXELTOAD_MOCK_CONTROL_URL="$MOCK_CONTROL_URL" \
  npm --prefix "$ROOT/sdk/typescript" run test:e2e
RESULT=$?
set -e 2>/dev/null || true

if [ "$RESULT" -eq 0 ]; then
  echo "$(green 'PASS') — data-plane SDK contract test passed against the live gateway"
else
  echo "$(red 'FAIL') — contract test failed (see output above)"
fi
exit "$RESULT"
