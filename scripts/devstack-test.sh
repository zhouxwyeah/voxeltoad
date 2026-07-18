#!/usr/bin/env bash
# End-to-end test + demo for the data-plane gateway (pairs with cmd/devstack).
#
# It prints each response AND asserts on status codes / bodies, exiting non-zero
# on the first failure — so it doubles as a human-readable demo and a smoke/
# regression gate.
#
# Modes:
#   ./scripts/devstack-test.sh            # auto: starts devstack, tests, stops it
#   GATEWAY=http://host:8080 KEY=sk-... MODEL=chat ./scripts/devstack-test.sh
#                                          # external: tests an already-running gateway
#
# In auto mode it needs nothing running (embedded PostgreSQL + mock upstream are
# in-process). First run downloads a PostgreSQL binary (cached afterwards).
set -uo pipefail

GATEWAY="${GATEWAY:-}"
KEY="${KEY:-sk-devstack-client}"
MODEL="${MODEL:-chat}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEVSTACK_PID=""
DEVSTACK_BIN=""

# --- test harness ------------------------------------------------------------
PASS=0
FAIL=0

green() { printf '\033[1;32m%s\033[0m' "$1"; }
red()   { printf '\033[1;31m%s\033[0m' "$1"; }
hr()    { printf '\n\033[1;34m== %s ==\033[0m\n' "$1"; }

ok() { PASS=$((PASS+1)); printf '  %s %s\n' "$(green PASS)" "$1"; }
no() { FAIL=$((FAIL+1)); printf '  %s %s\n    %s\n' "$(red FAIL)" "$1" "$2"; }

# assert_eq NAME EXPECTED ACTUAL
assert_eq() {
  if [ "$2" = "$3" ]; then ok "$1"; else no "$1" "expected [$2], got [$3]"; fi
}
# assert_contains NAME NEEDLE HAYSTACK
assert_contains() {
  case "$3" in
    *"$2"*) ok "$1" ;;
    *)      no "$1" "expected to contain [$2], got [$3]" ;;
  esac
}

# --- lifecycle ---------------------------------------------------------------
# We run a pre-built binary (not `go run`) so $DEVSTACK_PID is the devstack
# process itself — `go run` would fork/exec a child compiled binary and swallow
# the signal, orphaning the embedded PostgreSQL it spawns. On top of that we
# send the TERM to the whole process group as a belt-and-suspenders cleanup, and
# wait for graceful shutdown (devstack's defers stop PG + remove its data dir).
cleanup() {
  if [ -n "$DEVSTACK_PID" ] && kill -0 "$DEVSTACK_PID" 2>/dev/null; then
    # Try the process group first (negative PID), then the process directly.
    kill -TERM "-$DEVSTACK_PID" 2>/dev/null || kill -TERM "$DEVSTACK_PID" 2>/dev/null || true
    # Give devstack up to ~10s to stop PostgreSQL and clean up.
    for _ in $(seq 1 100); do
      kill -0 "$DEVSTACK_PID" 2>/dev/null || break
      sleep 0.1
    done
    kill -KILL "-$DEVSTACK_PID" 2>/dev/null || kill -KILL "$DEVSTACK_PID" 2>/dev/null || true
    wait "$DEVSTACK_PID" 2>/dev/null || true
  fi
  [ -n "$DEVSTACK_BIN" ] && rm -f "$DEVSTACK_BIN" 2>/dev/null || true
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
  DEVSTACK_BIN="$(mktemp -t devstack.XXXXXX)"
  if ! ( cd "$ROOT" && go build -tags devstack -o "$DEVSTACK_BIN" ./cmd/devstack ) 2>/tmp/devstack-build.log; then
    echo "$(red 'devstack build failed'):"; cat /tmp/devstack-build.log; exit 1
  fi

  echo "starting devstack (embedded PG + mock upstream; first run downloads PG)..."
  # Start in a fresh session/process group so cleanup can signal the whole tree.
  # setsid exists on Linux; macOS lacks it, so fall back to a plain background
  # job (the direct-binary approach already makes $! the devstack process).
  # GATEWAY_ALLOW_INSECURE_DEV=1: devstack intentionally runs with an open snapshot
  # channel (no admin plane, no internal token) — ADR-0007 dev escape hatch.
  if command -v setsid >/dev/null 2>&1; then
    setsid env GATEWAY_ALLOW_INSECURE_DEV=1 "$DEVSTACK_BIN" >/tmp/devstack-test.log 2>&1 &
  else
    env GATEWAY_ALLOW_INSECURE_DEV=1 "$DEVSTACK_BIN" >/tmp/devstack-test.log 2>&1 &
  fi
  DEVSTACK_PID=$!
  if ! wait_ready; then
    echo "$(red 'devstack did not become ready'). Last log lines:"
    tail -n 20 /tmp/devstack-test.log
    exit 1
  fi
else
  echo "testing external gateway at $GATEWAY"
  wait_ready || { echo "$(red "gateway at $GATEWAY not reachable")"; exit 1; }
fi

# chat_req MODEL STREAM AUTH  → prints "BODY\nHTTP_CODE"; split with code_of/body_of.
chat_req() {
  local model="$1" stream="$2" auth="$3"
  local data="{\"model\":\"$model\",\"stream\":$stream,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
  if [ "$auth" = "noauth" ]; then
    curl -sS -m 10 -w '\n%{http_code}' "$GATEWAY/v1/chat/completions" \
      -H "Content-Type: application/json" -d "$data"
  else
    curl -sS -m 10 -w '\n%{http_code}' "$GATEWAY/v1/chat/completions" \
      -H "Authorization: Bearer $auth" -H "Content-Type: application/json" -d "$data"
  fi
}
code_of() { printf '%s' "$1" | tail -n1; }
body_of() { printf '%s' "$1" | sed '$d'; }

echo "running against $GATEWAY (model=$MODEL, key=$KEY)"

# 1. health check
hr "1. health check (no auth)"
h="$(curl -sS -m 5 -w '\n%{http_code}' "$GATEWAY/healthz")"
echo "$(body_of "$h")"
assert_eq "healthz returns 200" "200" "$(code_of "$h")"
assert_eq "healthz body is ok"  "ok"  "$(body_of "$h")"

# 2. non-streaming chat completion
hr "2. chat completion (non-streaming)"
r="$(chat_req "$MODEL" false "$KEY")"
echo "$(body_of "$r")"
assert_eq       "chat non-stream 200"         "200" "$(code_of "$r")"
assert_contains "chat non-stream has content" '"content":"' "$(body_of "$r")"
assert_contains "chat non-stream bills usage" '"total_tokens":18' "$(body_of "$r")"

# 3. streaming chat completion (SSE)
hr "3. chat completion (streaming SSE)"
s="$(curl -sS -N -m 10 "$GATEWAY/v1/chat/completions" \
      -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
      -d "{\"model\":\"$MODEL\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}")"
echo "$s"
assert_contains "stream sends chunks"       'chat.completion.chunk' "$s"
assert_contains "stream carries first role" '"role":"assistant"'    "$s"
assert_contains "stream terminates [DONE]"  '[DONE]'                "$s"

# 4. missing key -> 401
hr "4. missing key -> 401"
r="$(chat_req "$MODEL" false noauth)"
echo "HTTP $(code_of "$r")"
assert_eq "missing key -> 401" "401" "$(code_of "$r")"

# 5. bad key -> 401
hr "5. invalid key -> 401"
r="$(chat_req "$MODEL" false "sk-does-not-exist")"
echo "HTTP $(code_of "$r")"
assert_eq "invalid key -> 401" "401" "$(code_of "$r")"

# 6. unknown model -> 502 (no route configured)
hr "6. unknown model -> 502 (no route)"
r="$(chat_req "no-such-model" false "$KEY")"
echo "HTTP $(code_of "$r")"
assert_eq "unknown model -> 502" "502" "$(code_of "$r")"

# --- summary -----------------------------------------------------------------
echo
if [ "$FAIL" -eq 0 ]; then
  echo "$(green "ALL PASSED") ($PASS assertions)"
  exit 0
else
  echo "$(red "FAILED"): $FAIL failed, $PASS passed"
  exit 1
fi
