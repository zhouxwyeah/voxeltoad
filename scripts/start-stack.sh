#!/usr/bin/env bash
# One-shot dev startup: starts adminstack (embedded PG + admin plane on
# 127.0.0.1:8090) in the background, waits for health, then runs Next.js dev
# server in the foreground. Ctrl-C stops everything.
#
#   make start-stack            # builds adminstack, starts everything
#   ./scripts/start-stack.sh    # same, from the repo root
#
# Needs nothing running beforehand — no Docker, no admin plane, no PG.
# First run downloads a PostgreSQL binary (cached afterwards).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_DIR="$ROOT/web"
WEB_PORT="${WEB_PORT:-3000}"

# Admin plane defaults — must match cmd/adminstack/main.go.
ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:8090}"

# Session secret for iron-session (32+ chars, dev-only, ephemeral).
SESSION_SECRET="${SESSION_SECRET:-dev-only-change-me-min-32-characters-long}"

red()   { printf '\033[1;31m%s\033[0m' "$1"; }
green() { printf '\033[1;32m%s\033[0m' "$1"; }
yellow(){ printf '\033[1;33m%s\033[0m' "$1"; }

STACK_PID=""
STACK_BIN=""
WEB_PID=""

# Kill any stale Next dev/start server left by a previous run whose parent died
# without firing the cleanup trap (next dev spawns worker processes that can
# survive Ctrl-C and keep port 3000 reserved).
kill_orphan_web() {
  pgrep -fl "next" 2>/dev/null | while read -r line; do
    case "$line" in
      *"next dev"*|*"next-server"*|*"next start"*)
        local pid="${line%% *}"
        echo "  stopping stale Next dev server (pid $pid)"
        kill "$pid" 2>/dev/null || true ;;
    esac
  done
  [ -z "$(pgrep -fl 'next' 2>/dev/null)" ] || sleep 0.5
}

# Teardown: kill the web-dev and adminstack process trees so the embedded PG
# stops cleanly, port 3000 is released, and the temp binary is removed.
cleanup() {
  echo
  echo "start-stack: stopping..."
  if [ -n "$WEB_PID" ] && kill -0 "$WEB_PID" 2>/dev/null; then
    kill -TERM "$WEB_PID" 2>/dev/null || true
    for _ in $(seq 1 50); do
      kill -0 "$WEB_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "$WEB_PID" 2>/dev/null || true
    wait "$WEB_PID" 2>/dev/null || true
    echo "start-stack: web stopped"
  fi
  if [ -n "$STACK_PID" ] && kill -0 "$STACK_PID" 2>/dev/null; then
    kill -TERM "$STACK_PID" 2>/dev/null || true
    for _ in $(seq 1 100); do
      kill -0 "$STACK_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "$STACK_PID" 2>/dev/null || true
    wait "$STACK_PID" 2>/dev/null || true
    echo "start-stack: adminstack stopped"
  fi
  [ -n "$STACK_BIN" ] && rm -f "$STACK_BIN" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# wait_url polls until the given URL responds with 2xx or times out.
wait_url() { # wait_url URL
  printf "  waiting for %s ..." "$1"
  for _ in $(seq 1 90); do
    if curl -sS -o /dev/null -m 1 "$1" 2>/dev/null; then
      printf " %s\n" "$(green "ready")"
      return 0
    fi
    sleep 1
  done
  printf "\n"
  return 1
}

# ---------------------------------------------------------------------------
# 1. Build adminstack binary (guarded tag so it stays out of normal build)
# ---------------------------------------------------------------------------
echo "start-stack: building adminstack (tag=adminstack)..."
STACK_BIN="$(mktemp -t adminstack.XXXXXX)"
cd "$ROOT"
if ! go build -tags adminstack -o "$STACK_BIN" ./cmd/adminstack 2>/tmp/start-stack-build.log; then
  echo "$(red 'adminstack build failed'):"
  cat /tmp/start-stack-build.log
  exit 1
fi
echo "  $(green "ok")"

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

# ---------------------------------------------------------------------------
# 2. Start adminstack in background, wait for healthz
# ---------------------------------------------------------------------------
echo "start-stack: booting adminstack (embedded PG, first run downloads binary)..."
if curl -sS -o /dev/null -m 1 "$ADMIN_URL/healthz" 2>/dev/null; then
  echo "  $(yellow "adminstack: port 8090 in use — stopping the stale process")"
  kill_orphan_pg
  kill_port 8090
fi

# Use setsid on Linux to create a clean process group; macOS lacks it so
# fall back to a plain background job (the pre-built binary approach keeps
# $! as the real process).
if command -v setsid >/dev/null 2>&1; then
  setsid "$STACK_BIN" >/tmp/start-stack-adminstack.log 2>&1 &
else
  "$STACK_BIN" >/tmp/start-stack-adminstack.log 2>&1 &
fi
STACK_PID=$!

if ! wait_url "$ADMIN_URL/healthz"; then
  echo "$(red 'adminstack not ready'). Last log:"
  tail -n 20 /tmp/start-stack-adminstack.log
  exit 1
fi
echo "  admin API → $(green "$ADMIN_URL")"

# ---------------------------------------------------------------------------
# 3. Web deps — ensure SDK is built + web deps installed (idempotent)
# ---------------------------------------------------------------------------
if [ ! -d "$WEB_DIR/node_modules" ]; then
  echo "start-stack: installing web dependencies..."
  make -s -C "$ROOT" web-install >/tmp/start-stack-web-install.log 2>&1 || {
    echo "$(red 'web-install failed')"; cat /tmp/start-stack-web-install.log; exit 1
  }
  echo "  $(green "ok")"
fi

# Copy .env.local from example if not present (idempotent; cp -n = no clobber).
cp -n "$WEB_DIR/.env.example" "$WEB_DIR/.env.local" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 4. Web dev server (Next.js, hot reload) on $WEB_PORT. Free a stale port
#    first so a previous run's orphaned Next dev server can't reserve 3000 or
#    force Next to auto-bump to 3001.
# ---------------------------------------------------------------------------
echo "start-stack: freeing web port $WEB_PORT if a stale Next dev server holds it..."
kill_orphan_web
kill_port "$WEB_PORT"
echo
echo "  _____  _  _  _       __  __           _        _____       _"
echo " | ____|| || || |     |  \\/  |  ___    | |      / ____|     | |"
echo " | |__  | || || |     | |\\\\/| | / _ \\   | |     | |  __  ___ | |_  ___  _   _  ___  _ __"
echo " | __|  | || || |     | |  | ||  __/   | |     | | |_ |/ _ \\| __|/ _ \\| | | |/ _ \\| '_ \\"
echo " | |___ |_||_||_|     |_|  |_| \\___|   |_|     | |__| | (_) | |_| (_) | |_| | (_) | | | |"
echo " |_____|                                    \\_____|\\___/ \\__|\\___/ \\__, |\\___/|_| |_|"
echo "                                                                    __/ |"
echo "                                                                   |___/"
echo
echo "  admin API → http://127.0.0.1:8090"
echo "  web  UI   → http://localhost:$WEB_PORT"
echo "  login     → super-admin: root@adminstack / adminstack-pass-123"
echo "             tenant-admin: tenant-admin@demo / demo-pass-123"
echo "             api key:      sk-demo-tenant-key-0001  (data plane)"
echo
echo "  demo data is seeded on a fresh start (providers/models/routes/tenant/key/quota)."
echo "  GATEWAY_PERSIST_DATA=1  keep data across restarts (default: ephemeral, wiped on exit)"
echo "  GATEWAY_SEED_DEMO=0     skip seeding demo data (default: seed)"
echo
echo "  Ctrl-C to stop everything."
echo

# Run the Next.js dev server in the background (tracked via WEB_PID) so the
# EXIT/INT/TERM trap can clean it up together with adminstack. The job inherits
# stdout/stderr, so HMR logs still print to the terminal. `wait` keeps the shell
# in the foreground and lets the trap reap both process trees on Ctrl-C.
cd "$WEB_DIR"
if command -v setsid >/dev/null 2>&1; then
  setsid env ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" PORT="$WEB_PORT" npm run dev &
else
  env ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" PORT="$WEB_PORT" npm run dev &
fi
WEB_PID=$!
wait "$WEB_PID"
