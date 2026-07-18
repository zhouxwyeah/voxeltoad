#!/usr/bin/env bash
# End-to-end test for the Control Panel UI (slice 0) — drives a real browser
# through the whole stack: browser → Next (RSC/Server Actions + encrypted-cookie
# session) → admin API. Pairs with cmd/adminstack and web/tests/e2e.
#
# It starts:
#   1. adminstack  — real admin plane + embedded PostgreSQL on 127.0.0.1:8090,
#      bootstrapped super-admin (root@adminstack / adminstack-pass-123).
#   2. the Next web server (production build) with ADMIN_URL + SESSION_SECRET.
# then runs Playwright against the Next server, and tears everything down.
#
# Modes:
#   ./scripts/web-e2e.sh                         # auto: start both, test, stop
#   WEB_BASE_URL=http://host:3000 ADMIN_URL=... ./scripts/web-e2e.sh
#                                                # external: test running servers
#
# Auto mode needs nothing running (embedded PG is in-process; first run
# downloads a PG binary + Playwright's chromium, both cached afterwards).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_DIR="$ROOT/web"

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:8090}"
ADMIN_EMAIL="${VOXELTOAD_ADMIN_EMAIL:-root@adminstack}"
ADMIN_PASSWORD="${VOXELTOAD_ADMIN_PASSWORD:-adminstack-pass-123}"
WEB_PORT="${WEB_PORT:-3000}"
WEB_BASE_URL="${WEB_BASE_URL:-http://127.0.0.1:$WEB_PORT}"
# 32+ char secret for iron-session (ephemeral, test-only).
SESSION_SECRET="${SESSION_SECRET:-e2e-only-session-secret-min-32-chars-xxxxx}"

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }

STACK_PID=""
STACK_BIN=""
WEB_PID=""

# Run pre-built binaries (not `go run`/`next dev` background children) so the
# PIDs are the real processes; also signal the whole process group + wait for
# graceful shutdown (adminstack's defers stop PG + remove its dir).
cleanup() {
  if [ -n "$WEB_PID" ] && kill -0 "$WEB_PID" 2>/dev/null; then
    kill -TERM "-$WEB_PID" 2>/dev/null || kill -TERM "$WEB_PID" 2>/dev/null || true
    for _ in $(seq 1 50); do kill -0 "$WEB_PID" 2>/dev/null || break; sleep 0.1; done
    kill -KILL "-$WEB_PID" 2>/dev/null || kill -KILL "$WEB_PID" 2>/dev/null || true
    wait "$WEB_PID" 2>/dev/null || true
  fi
  if [ -n "$STACK_PID" ] && kill -0 "$STACK_PID" 2>/dev/null; then
    kill -TERM "-$STACK_PID" 2>/dev/null || kill -TERM "$STACK_PID" 2>/dev/null || true
    for _ in $(seq 1 100); do kill -0 "$STACK_PID" 2>/dev/null || break; sleep 0.1; done
    kill -KILL "-$STACK_PID" 2>/dev/null || kill -KILL "$STACK_PID" 2>/dev/null || true
    wait "$STACK_PID" 2>/dev/null || true
  fi
  [ -n "$STACK_BIN" ] && rm -f "$STACK_BIN" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

wait_url() { # wait_url URL
  for _ in $(seq 1 90); do
    if curl -sS -o /dev/null -m 1 "$1" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

# assert_port_free fails fast if a server is ALREADY responding at URL — the
# silent-fallthrough trap. Without this, if our freshly-built server can't bind
# (EADDRINUSE because the user's `make adminstack` / `next dev` is still there),
# wait_url would pass against the STALE server and Playwright would test the
# wrong thing (hit during restyle verification: the user's `next dev` on :3000
# made `next start` fail to bind, and the suite silently drove the dev server
# where client onClick doesn't hydrate → false red). Pre-checking before start
# avoids the timing gap (adminstack's bind failure only logs after PG startup).
assert_port_free() { # assert_port_free URL LABEL
  if curl -sS -o /dev/null -m 1 "$1" 2>/dev/null; then
    echo "$(red "$2: a server is already responding at $1.")"
    echo "$(red 'Aborting so we never silently test a stale server.')"
    echo "  → stop the other instance (or free the port) and re-run."
    return 1
  fi
  return 0
}

# --- 1. admin plane (embedded PG) ---
echo "building adminstack binary..."
STACK_BIN="$(mktemp -t adminstack.XXXXXX)"
if ! ( cd "$ROOT" && go build -tags adminstack -o "$STACK_BIN" ./cmd/adminstack ) 2>/tmp/web-e2e-adminbuild.log; then
  echo "$(red 'adminstack build failed'):"; cat /tmp/web-e2e-adminbuild.log; exit 1
fi
echo "starting adminstack (embedded PG; first run downloads PG)..."
assert_port_free "$ADMIN_URL/healthz" "adminstack" || exit 1
if command -v setsid >/dev/null 2>&1; then
  setsid "$STACK_BIN" >/tmp/web-e2e-adminstack.log 2>&1 &
else
  "$STACK_BIN" >/tmp/web-e2e-adminstack.log 2>&1 &
fi
STACK_PID=$!
if ! wait_url "$ADMIN_URL/healthz"; then
  echo "$(red 'adminstack not ready'). Last log:"; tail -n 20 /tmp/web-e2e-adminstack.log; exit 1
fi
echo "admin plane ready at $ADMIN_URL"

# --- 2. SDK + web deps + build ---
echo "building admin SDK (file: dependency)..."
( cd "$ROOT" && make sdk-build >/tmp/web-e2e-sdk.log 2>&1 ) || { echo "$(red 'sdk build failed')"; cat /tmp/web-e2e-sdk.log; exit 1; }

if [ ! -d "$WEB_DIR/node_modules" ]; then
  echo "installing web dependencies..."
  ( cd "$WEB_DIR" && npm install --silent ) || { echo "$(red 'npm install failed')"; exit 1; }
fi

echo "ensuring Playwright chromium is installed..."
( cd "$WEB_DIR" && npx playwright install chromium >/tmp/web-e2e-pw.log 2>&1 ) || { echo "$(red 'playwright install failed')"; cat /tmp/web-e2e-pw.log; exit 1; }

echo "building web..."
( cd "$WEB_DIR" && ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" npm run build >/tmp/web-e2e-webbuild.log 2>&1 ) || { echo "$(red 'web build failed')"; tail -n 30 /tmp/web-e2e-webbuild.log; exit 1; }

# --- 3. Next server ---
echo "starting web server on :$WEB_PORT..."
assert_port_free "$WEB_BASE_URL/login" "web server" || exit 1
if command -v setsid >/dev/null 2>&1; then
  setsid env ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" PORT="$WEB_PORT" npm --prefix "$WEB_DIR" run start >/tmp/web-e2e-web.log 2>&1 &
else
  env ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" PORT="$WEB_PORT" npm --prefix "$WEB_DIR" run start >/tmp/web-e2e-web.log 2>&1 &
fi
WEB_PID=$!
if ! wait_url "$WEB_BASE_URL/login"; then
  echo "$(red 'web server not ready'). Last log:"; tail -n 20 /tmp/web-e2e-web.log; exit 1
fi
echo "web ready at $WEB_BASE_URL — running Playwright"

# --- 4. Playwright ---
set +e
WEB_BASE_URL="$WEB_BASE_URL" \
VOXELTOAD_ADMIN_EMAIL="$ADMIN_EMAIL" VOXELTOAD_ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  npm --prefix "$WEB_DIR" run test:e2e
RESULT=$?
set -e 2>/dev/null || true

if [ "$RESULT" -eq 0 ]; then
  echo "$(green 'PASS') — Control Panel slice 0 e2e passed against the live stack"
else
  echo "$(red 'FAIL') — slice 0 e2e failed (see output above)"
fi
exit "$RESULT"
