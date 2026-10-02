#!/usr/bin/env bash
# Run ALL THREE stack tests against ONE shared embedded PostgreSQL.
#
# Why this exists: devstack-test / sdk-chat-e2e / adminstack-test each
# previously booted their own embedded PG (~3-4s initdb+boot) and rebuilt the
# same binaries, adding ~30-60s to `make ci`. This script builds each binary
# ONCE, starts ONE shared PG (cmd/testpg), then drives the three suites:
#
#   1. devstack smoke test   (scripts/devstack-test.sh, external mode)
#   2. sdk-chat-e2e          (scripts/devstack-sdk-e2e.sh, external mode)
#   3. adminstack contract   (scripts/adminstack-test.sh, external mode)
#
# Suites (1) and (2) share the SAME devstack process (both talk to the same
# gateway :8080 and mock-control :8091), so they run back-to-back without a
# restart. adminstack gets its own database (voxeltoad_adminstack) inside the same
# PG. Isolation between suites is schema-level (separate databases), which is
# sufficient because each suite seeds its own fixtures and asserts on its own
# responses — none assert on absolute row counts across the whole DB.
#
# Run it directly, or via `make stack-test-all` / `make ci`.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GATEWAY="http://127.0.0.1:8080"
ADMIN_URL="http://127.0.0.1:8090"
PG_PORT=55431
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/voxeltoad-stack-test.XXXXXX")" || exit 1
LAUNCHED_PID=""

TESTPG_BIN=""
DEVSTACK_BIN=""
ADMINSTACK_BIN=""
TESTPG_PID=""
DEVSTACK_PID=""
ADMINSTACK_PID=""

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }
hr()    { printf '\n\033[1;34m== %s ==\033[0m\n' "$1"; }

# TERM a pid (process group first, then direct) and wait for graceful exit.
stop_proc() {
  local pid="$1"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 100); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
}

cleanup() {
  stop_proc "$ADMINSTACK_PID"
  stop_proc "$DEVSTACK_PID"
  stop_proc "$TESTPG_PID"
  [ -n "$TESTPG_BIN" ] && rm -f "$TESTPG_BIN" 2>/dev/null || true
  [ -n "$DEVSTACK_BIN" ] && rm -f "$DEVSTACK_BIN" 2>/dev/null || true
  [ -n "$ADMINSTACK_BIN" ] && rm -f "$ADMINSTACK_BIN" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Fail if our child exited instead of testing an unrelated/stale listener.
wait_url() {
  local url="$1" pid="$2"
  for _ in $(seq 1 90); do
    kill -0 "$pid" 2>/dev/null || return 1
    if curl -fsS -o /dev/null -m 1 "$url" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

# TCP readiness precedes CREATE DATABASE; wait for testpg's ready banner.
wait_pg() {
  local line
  for _ in $(seq 1 90); do
    kill -0 "$TESTPG_PID" 2>/dev/null || return 1
    while IFS= read -r line; do
      case "$line" in 'testpg ready'*) return 0 ;; esac
    done <"$RUN_DIR/pg.log"
    sleep 1
  done
  return 1
}

launch() { # launch BIN LOG ENV...
  local bin="$1" log="$2"; shift 2
  if command -v setsid >/dev/null 2>&1; then
    setsid env "$@" "$bin" >"$log" 2>&1 &
  else
    env "$@" "$bin" >"$log" 2>&1 &
  fi
  LAUNCHED_PID=$!
}

# Refuse occupied ports before testpg can touch any database. TMPDIR below also
# confines its fixed runtime directory to a fresh directory owned by this run.
for port in "$PG_PORT" 8080 8090 8091; do
  if ( : >"/dev/tcp/127.0.0.1/$port" ) 2>/dev/null; then
    printf '%s\n' "Port $port is already occupied; refusing to reuse another stack."
    exit 1
  fi
done

# --- build (each binary exactly once) ----------------------------------------
hr "build binaries (testpg / devstack / adminstack)"
TESTPG_BIN="$RUN_DIR/testpg"
DEVSTACK_BIN="$RUN_DIR/devstack"
ADMINSTACK_BIN="$RUN_DIR/adminstack"
( cd "$ROOT" && go build -tags testpg -o "$TESTPG_BIN" ./cmd/testpg ) \
  || { echo "$(red 'testpg build failed')"; exit 1; }
( cd "$ROOT" && go build -tags devstack -o "$DEVSTACK_BIN" ./cmd/devstack ) \
  || { echo "$(red 'devstack build failed')"; exit 1; }
( cd "$ROOT" && go build -tags adminstack -o "$ADMINSTACK_BIN" ./cmd/adminstack ) \
  || { echo "$(red 'adminstack build failed')"; exit 1; }

# --- shared PG ---------------------------------------------------------------
hr "start shared embedded PostgreSQL (cmd/testpg)"
launch "$TESTPG_BIN" "$RUN_DIR/pg.log" TMPDIR="$RUN_DIR"
TESTPG_PID="$LAUNCHED_PID"
if ! wait_pg; then
  echo "$(red 'shared PG did not become ready'). Last log lines:"
  tail -n 20 "$RUN_DIR/pg.log"
  exit 1
fi
DEVSTACK_DSN="postgres://postgres:postgres@localhost:$PG_PORT/voxeltoad_devstack?sslmode=disable"
ADMINSTACK_DSN="postgres://postgres:postgres@localhost:$PG_PORT/voxeltoad_adminstack?sslmode=disable"

FAILED=0

# --- devstack (one process, drives suites 1+2) -------------------------------
hr "start devstack against shared PG"
launch "$DEVSTACK_BIN" "$RUN_DIR/devstack.log" \
  GATEWAY_ALLOW_INSECURE_DEV=1 GATEWAY_PG_DSN="$DEVSTACK_DSN"
DEVSTACK_PID="$LAUNCHED_PID"
if ! wait_url "$GATEWAY/healthz" "$DEVSTACK_PID"; then
  echo "$(red 'devstack did not become ready'). Last log lines:"
  tail -n 20 "$RUN_DIR/devstack.log"
  exit 1
fi

hr "suite 1/3: devstack smoke test"
GATEWAY="$GATEWAY" "$ROOT/scripts/devstack-test.sh" || FAILED=1

hr "suite 2/3: data-plane SDK contract test"
GATEWAY="$GATEWAY" "$ROOT/scripts/devstack-sdk-e2e.sh" || FAILED=1

# --- adminstack (suite 3) ----------------------------------------------------
hr "start adminstack against shared PG"
launch "$ADMINSTACK_BIN" "$RUN_DIR/adminstack.log" GATEWAY_PG_DSN="$ADMINSTACK_DSN" GATEWAY_SEED_DEMO=0 GATEWAY_PERSIST_DATA=0
ADMINSTACK_PID="$LAUNCHED_PID"
if ! wait_url "$ADMIN_URL/healthz" "$ADMINSTACK_PID"; then
  echo "$(red 'adminstack did not become ready'). Last log lines:"
  tail -n 20 "$RUN_DIR/adminstack.log"
  exit 1
fi

hr "suite 3/3: Control Panel contract test"
ADMIN_URL="$ADMIN_URL" "$ROOT/scripts/adminstack-test.sh" || FAILED=1

# --- summary -----------------------------------------------------------------
echo
if [ "$FAILED" -eq 0 ]; then
  echo "$(green 'ALL 3 SUITES PASSED') — one shared PG, one build each"
  exit 0
else
  echo "$(red 'FAILED') — see suite output above"
  exit 1
fi
