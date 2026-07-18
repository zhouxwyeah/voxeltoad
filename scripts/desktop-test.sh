#!/usr/bin/env bash
# Manual smoke test for the desktop personal gateway (cmd/desktop).
#
# Spins up a mock upstream + the desktop gateway, drives a real chat request
# through the data plane (/v1/chat/completions), then verifies the recording
# landed in SQLite via the read API (/api/v1/request-logs). Exits non-zero on
# the first failure so it doubles as a human-readable demo + a regression gate.
#
# Mirrors scripts/devstack-test.sh shape. Unlike devstack, the desktop gateway
# has no embedded PostgreSQL or in-process mock upstream — we run a standalone
# mock-upstream binary (test/mock-upstream) as a sibling process.
#
# Run:   ./scripts/desktop-test.sh
# Env:   GATEWAY_PORT (default 8787), MOCK_PORT (default 8099), KEY (default
#        desktop-local-default-key, matches seed.DefaultKey())
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GATEWAY_PORT="${GATEWAY_PORT:-8787}"
MOCK_PORT="${MOCK_PORT:-8099}"
KEY="${KEY:-desktop-local-default-key}"
MODEL="${MODEL:-default}"

GATEWAY="http://127.0.0.1:$GATEWAY_PORT"
MOCK_URL="http://127.0.0.1:$MOCK_PORT"

DESKTOP_PID=""
MOCK_PID=""
DESKTOP_BIN=""
MOCK_BIN=""
WORKDIR=""
CFG_PATH=""

green() { printf '\033[1;32m%s\033[0m' "$1"; }
red()   { printf '\033[1;31m%s\033[0m' "$1"; }
hr()    { printf '\n\033[1;34m== %s ==\033[0m\n' "$1"; }

# Split "BODY\nHTTP_CODE" (curl -w '\n%{http_code}') into two vars, BSD-head-safe.
split_resp() {
  # $1 = input var name, $2 = body var name, $3 = code var name
  local _in _code
  _in="$1"
  _code="$(printf '%s' "$_in" | tail -n1)"
  printf -v "$2" "$(printf '%s' "$_in" | sed '$d')"
  printf -v "$3" '%s' "$_code"
}

PASS=0
FAIL=0
ok() { PASS=$((PASS+1)); printf '  %s %s\n' "$(green PASS)" "$1"; }
no() { FAIL=$((FAIL+1)); printf '  %s %s\n    %s\n' "$(red FAIL)" "$1" "$2"; }

cleanup() {
  [ -n "$DESKTOP_PID" ] && kill -TERM "$DESKTOP_PID" 2>/dev/null
  [ -n "$MOCK_PID" ]    && kill -TERM "$MOCK_PID"    2>/dev/null
  wait "$DESKTOP_PID" 2>/dev/null || true
  wait "$MOCK_PID"    2>/dev/null || true
  [ -n "$DESKTOP_BIN" ] && rm -f "$DESKTOP_BIN" 2>/dev/null || true
  [ -n "$MOCK_BIN" ]    && rm -f "$MOCK_BIN"    2>/dev/null || true
  [ -n "$WORKDIR" ]     && rm -rf "$WORKDIR"     2>/dev/null || true
}
trap cleanup EXIT INT TERM

wait_ready() {
  local url="$1"
  for _ in $(seq 1 30); do
    if curl -sS -o /dev/null -m 1 "$url" 2>/dev/null; then return 0; fi
    sleep 0.5
  done
  return 1
}

hr "building binaries"
WORKDIR="$(mktemp -d)"
DESKTOP_BIN="$WORKDIR/desktop"
MOCK_BIN="$WORKDIR/mock-upstream"
CFG_PATH="$WORKDIR/desktop.yaml"

echo "  building mock-upstream..."
if ! ( cd "$ROOT" && go build -o "$MOCK_BIN" ./test/mock-upstream ) 2>"$WORKDIR/mock-build.log"; then
  echo "$(red 'mock-upstream build failed'):"; cat "$WORKDIR/mock-build.log"; exit 1
fi

echo "  building desktop gateway..."
if ! ( cd "$ROOT" && go build -o "$DESKTOP_BIN" ./cmd/desktop ) 2>"$WORKDIR/desktop-build.log"; then
  echo "$(red 'desktop build failed'):"; cat "$WORKDIR/desktop-build.log"; exit 1
fi

hr "writing config (provider=mock-upstream)"
cat >"$CFG_PATH" <<EOF
gateway:
  addr: "127.0.0.1:$GATEWAY_PORT"
  session_headers:
    - X-Voxeltoad-Session
providers:
  - name: mock
    type: openai
    adapter: openai
    base_url: "$MOCK_URL"
    api_key_ref: "plain://mock-key"
    weight: 1
models:
  - alias: default
    upstreams:
      - provider: mock
        upstream_model: mock-model
routes:
  - model_alias: default
    providers:
      - name: mock
        weight: 1
    strategy: priority
settings:
  trace:
    capture_payload_enabled: true
    max_body_kb: 256
    retention_days: 30
EOF
echo "  config at $CFG_PATH"

hr "starting mock-upstream on $MOCK_URL"
"$MOCK_BIN" -addr "127.0.0.1:$MOCK_PORT" >/dev/null 2>&1 &
MOCK_PID=$!
wait_ready "$MOCK_URL/" || { echo "$(red 'mock-upstream not ready')"; exit 1; }
ok "mock-upstream up"

hr "starting desktop gateway on $GATEWAY"
DESKTOP_DB="$WORKDIR/desktop.db" \
  "$DESKTOP_BIN" -config "$CFG_PATH" -db "$WORKDIR/desktop.db" >"$WORKDIR/desktop.log" 2>&1 &
DESKTOP_PID=$!
if ! wait_ready "$GATEWAY/api/v1/health"; then
  echo "$(red 'desktop gateway not ready'). Last log lines:"
  tail -n 20 "$WORKDIR/desktop.log"
  exit 1
fi
ok "desktop gateway up (api key: $KEY)"

hr "non-streaming /v1/chat/completions"
RESP="$(curl -sS -m 10 -o - -w '\n%{http_code}' \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" \
  "$GATEWAY/v1/chat/completions")"
split_resp "$RESP" BODY CODE
if [ "$CODE" = "200" ]; then
  ok "chat status 200"
else
  no "chat status" "expected 200, got $CODE; body: $BODY"
fi
if echo "$BODY" | grep -q "hello from mock-upstream"; then
  ok "upstream content returned"
else
  no "upstream content" "body: $BODY"
fi

hr "streaming /v1/chat/completions"
SRESP="$(curl -sS -m 10 -o - -w '\n%{http_code}' \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"model\":\"$MODEL\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" \
  "$GATEWAY/v1/chat/completions")"
split_resp "$SRESP" SBODY SCODE
if [ "$SCODE" = "200" ]; then ok "stream status 200"; else no "stream status" "got $SCODE"; fi
if echo "$SBODY" | grep -q "data: \[DONE\]"; then
  ok "SSE stream terminated with [DONE]"
else
  no "SSE terminator" "body: $SBODY"
fi

# Give the async recorders a moment to flush to SQLite.
sleep 0.3

hr "read API: /api/v1/request-logs"
LRESP="$(curl -sS -m 5 -o - -w '\n%{http_code}' "$GATEWAY/api/v1/request-logs?page=1&page_size=10")"
split_resp "$LRESP" LBODY LCODE
if [ "$LCODE" = "200" ]; then ok "read API 200"; else no "read API" "status $LCODE"; fi
if echo "$LBODY" | grep -q '"provider":"mock"'; then
  ok "request_logs recorded with provider=mock"
else
  no "request_logs recording" "body: $LBODY"
fi

hr "read API: /api/v1/overview"
ORESP="$(curl -sS -m 5 -o - -w '\n%{http_code}' "$GATEWAY/api/v1/overview")"
split_resp "$ORESP" OBODY OCODE
if [ "$OCODE" = "200" ]; then ok "overview 200"; else no "overview" "status $OCODE"; fi

hr "result"
printf '  %s passed, %s failed\n' "$(green $PASS)" "$(red $FAIL)"
[ "$FAIL" -eq 0 ] || exit 1
echo
echo "desktop.log tail:"
tail -n 5 "$WORKDIR/desktop.log" 2>/dev/null || true
