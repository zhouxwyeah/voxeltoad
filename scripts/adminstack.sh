#!/usr/bin/env bash
# Foreground dev server for the management plane (Control Panel) — pairs with
# cmd/adminstack. Builds a pre-built binary (so $! is the real adminstack
# process, NOT a `go run` wrapper) and runs it until Ctrl-C, tearing down the
# embedded PostgreSQL it started on exit.
#
# Why not `go run`? `go run` forks a child binary and, on Ctrl-C / kill, the
# parent dies while the child + its embedded PostgreSQL survive as orphans.
# The adminstack binary itself shuts down PG gracefully on SIGINT/SIGTERM
# (see cmd/adminstack/main.go), so launching the compiled binary directly —
# and killing its whole process group on exit — guarantees no orphaned PG.
# This mirrors scripts/adminstack-test.sh (which drives it in e2e mode).
#
# Usage:
#   make adminstack            # foreground; Ctrl-C stops adminstack + embedded PG
#   ./scripts/adminstack.sh    # same
#
# Env switches (passed through to the binary):
#   GATEWAY_PERSIST_DATA=1  keep data across restarts (default: ephemeral)
#   GATEWAY_SEED_DEMO=0     skip seeding demo data (default: seed)
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STACK_PID=""
STACK_BIN=""

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$1"; }

# --- lifecycle ---------------------------------------------------------------
# Run a pre-built binary (not `go run`) so $STACK_PID is the adminstack process
# itself; `go run` would fork a child and swallow the signal, orphaning the
# embedded PostgreSQL. TERM the whole process group as belt-and-suspenders, and
# wait for graceful shutdown (adminstack's defers stop PG + remove its dir).
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

# kill_port kills any process currently bound to the given TCP port, waiting
# briefly for the socket to free and falling back to SIGKILL if needed.
kill_port() { # kill_port PORT
  local port="$1" pids
  pids="$(lsof -ti ":$port" 2>/dev/null || true)"
  [ -z "$pids" ] && return 0
  echo "  freeing port $port (pids: $(echo $pids | tr '\n' ' '))"
  echo "$pids" | xargs -r kill 2>/dev/null || true
  for _ in $(seq 1 50); do
    lsof -ti ":$port" >/dev/null 2>&1 || return 0
    sleep 0.1
  done
  echo "  port $port still busy — sending SIGKILL"
  lsof -ti ":$port" 2>/dev/null | xargs -r kill -9 2>/dev/null || true
  return 0
}

# kill_orphan_pg stops an embedded PostgreSQL left behind by a previous dev run
# whose adminstack parent died without firing the cleanup trap (it lives in a
# temp dir named voxeltoad-adminstack-pg-*). Safe to call before launching a new
# stack since the new embedded PG does not exist yet.
kill_orphan_pg() {
  pgrep -fl "voxeltoad-adminstack-pg" 2>/dev/null | while read -r line; do
    local pid="${line%% *}"
    echo "  stopping orphaned embedded PG (pid $pid)"
    kill "$pid" 2>/dev/null || true
  done
  [ -z "$(pgrep -fl 'voxeltoad-adminstack-pg' 2>/dev/null)" ] || sleep 0.5
}

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:8090}"

echo "building adminstack binary (tag=adminstack)..."
STACK_BIN="$(mktemp -t adminstack.XXXXXX)"
cd "$ROOT"
if ! go build -tags adminstack -o "$STACK_BIN" ./cmd/adminstack 2>/tmp/adminstack-build.log; then
  echo "$(red 'adminstack build failed'):"
  cat /tmp/adminstack-build.log
  exit 1
fi
echo "  $(green "ok")"

# Free a busy port (stale adminstack / orphaned PG) before booting, so a prior
# hard-kill never blocks this run with a bind collision.
if curl -sS -o /dev/null -m 1 "$ADMIN_URL/healthz" 2>/dev/null; then
  echo "  $(yellow "port 8090 in use — stopping the stale process")"
  kill_orphan_pg
  kill_port 8090
fi

echo "starting adminstack (embedded PG; first run downloads PG)..."
if command -v setsid >/dev/null 2>&1; then
  setsid "$STACK_BIN" &
else
  "$STACK_BIN" &
fi
STACK_PID=$!

# Block until the binary exits (Ctrl-C here triggers the cleanup trap, which
# TERM's the process group so the binary's defers stop PG + remove its dir).
wait "$STACK_PID"
