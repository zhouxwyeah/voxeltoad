#!/usr/bin/env bash
# End-to-end test for the management-plane (Control Panel) API — pairs with
# cmd/adminstack. It boots a real admin HTTP server backed by embedded
# PostgreSQL, then drives it through real admin workflows via the GENERATED
# TypeScript admin client (sdk/typescript, contract test), exercising the exact
# request/response types published in docs/openapi/admin.yaml.
#
# This is the runtime half of the ADR-0019 contract loop: the Go dbtest proves
# handler responses match the spec in-process; this proves the generated client
# talks to a real server over HTTP.
#
# Modes:
#   ./scripts/adminstack-test.sh                 # auto: starts adminstack, tests, stops it
#   ADMIN_URL=http://host:8090 ADMIN_EMAIL=... ADMIN_PASSWORD=... \
#     ./scripts/adminstack-test.sh               # external: test an already-running admin plane
#
# In auto mode nothing else needs to run (embedded PostgreSQL is in-process;
# first run downloads a PG binary, cached afterwards).
set -uo pipefail

ADMIN_URL="${ADMIN_URL:-}"
ADMIN_EMAIL="${ADMIN_EMAIL:-root@adminstack}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-adminstack-pass-123}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STACK_PID=""
STACK_BIN=""

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }

# --- lifecycle ---------------------------------------------------------------
# Run a pre-built binary (not `go run`) so $STACK_PID is the adminstack process
# itself; `go run` would fork a child and swallow the signal, orphaning the
# embedded PostgreSQL. Also TERM the whole process group as belt-and-suspenders,
# and wait for graceful shutdown (adminstack's defers stop PG + remove its dir).
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
    if curl -sS -o /dev/null -m 1 "$ADMIN_URL/healthz" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

if [ -z "$ADMIN_URL" ]; then
  ADMIN_URL="http://127.0.0.1:8090"
  echo "building adminstack binary..."
  STACK_BIN="$(mktemp -t adminstack.XXXXXX)"
  if ! ( cd "$ROOT" && go build -tags adminstack -o "$STACK_BIN" ./cmd/adminstack ) 2>/tmp/adminstack-build.log; then
    echo "$(red 'adminstack build failed'):"; cat /tmp/adminstack-build.log; exit 1
  fi

  echo "starting adminstack (embedded PG; first run downloads PG)..."
  if command -v setsid >/dev/null 2>&1; then
    setsid "$STACK_BIN" >/tmp/adminstack-test.log 2>&1 &
  else
    "$STACK_BIN" >/tmp/adminstack-test.log 2>&1 &
  fi
  STACK_PID=$!
  if ! wait_ready; then
    echo "$(red 'adminstack did not become ready'). Last log lines:"
    tail -n 20 /tmp/adminstack-test.log
    exit 1
  fi
else
  echo "testing external admin plane at $ADMIN_URL"
  wait_ready || { echo "$(red "admin plane at $ADMIN_URL not reachable")"; exit 1; }
fi

echo "admin plane ready at $ADMIN_URL — running generated-client contract test"

# Ensure SDK deps + generated types are present, then run the opt-in e2e test
# (vitest) that drives the generated admin client against the live server.
if [ ! -d "$ROOT/sdk/typescript/node_modules" ]; then
  echo "installing SDK dependencies..."
  ( cd "$ROOT/sdk/typescript" && npm install --silent ) || { echo "$(red 'npm install failed')"; exit 1; }
fi

echo "regenerating admin client types from the spec..."
( cd "$ROOT/sdk/typescript" && npm run --silent codegen ) || { echo "$(red 'codegen failed')"; exit 1; }

set +e
VOXELTOAD_ADMIN_E2E=1 \
VOXELTOAD_ADMIN_BASE_URL="$ADMIN_URL" \
VOXELTOAD_ADMIN_EMAIL="$ADMIN_EMAIL" \
VOXELTOAD_ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  npm --prefix "$ROOT/sdk/typescript" run test:e2e
RESULT=$?
set -e 2>/dev/null || true

if [ "$RESULT" -eq 0 ]; then
  echo "$(green 'PASS') — Control Panel contract test passed against the live admin plane"
else
  echo "$(red 'FAIL') — contract test failed (see output above)"
fi
exit "$RESULT"
