#!/usr/bin/env bash
# Browser → Next → admin plus a real data plane, all using one isolated PG.
# adminstack owns the ephemeral PG; devstack supplies its in-process mock.
# The existing gateway binary reads the admin snapshot and persists all ledgers
# to that same PG. Its bootstrap config is generated here, never read from a
# developer/production config file. The seeded mock model alias is chat.
#
# Auto: ./scripts/web-e2e.sh [Playwright arguments...]
# External: WEB_E2E_EXTERNAL=1 WEB_BASE_URL=... ADMIN_URL=... GATEWAY_URL=... \
#           ./scripts/web-e2e.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_DIR="$ROOT/web"
EXTERNAL="${WEB_E2E_EXTERNAL:-0}"
ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:8090}"
ADMIN_EMAIL="${VOXELTOAD_ADMIN_EMAIL:-root@adminstack}"
ADMIN_PASSWORD="${VOXELTOAD_ADMIN_PASSWORD:-adminstack-pass-123}"
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:12800}"
MOCK_CONTROL_URL="${MOCK_CONTROL_URL:-http://127.0.0.1:8091}"
WEB_PORT="${WEB_PORT:-3000}"
WEB_BASE_URL="${WEB_BASE_URL:-http://127.0.0.1:$WEB_PORT}"
SESSION_SECRET="${SESSION_SECRET:-e2e-only-session-secret-min-32-chars-xxxxx}"
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/voxeltoad-web-e2e.XXXXXX")" || exit 1
STACK_BIN="$RUN_DIR/adminstack"
DEVSTACK_BIN="$RUN_DIR/devstack"
GATEWAY_BIN="$RUN_DIR/gateway"
STACK_PID=""
DEVSTACK_PID=""
GATEWAY_PID=""
WEB_PID=""
LAUNCHED_PID=""

stop_proc() {
  local pid="$1"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 100); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  [ -z "$pid" ] || wait "$pid" 2>/dev/null || true
}

cleanup() {
  stop_proc "$WEB_PID"
  stop_proc "$GATEWAY_PID"
  stop_proc "$DEVSTACK_PID"
  # Stop PG last, after the data plane has drained its async ledgers.
  stop_proc "$STACK_PID"
  rm -f "$STACK_BIN" "$DEVSTACK_BIN" "$GATEWAY_BIN" "$RUN_DIR/gateway.yaml"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Do not launch inside command substitution: the PID must be a child of this
# shell, otherwise wait cannot reap it or observe a failed startup.
launch() {
  local log="$1"; shift
  if command -v setsid >/dev/null 2>&1; then
    setsid "$@" >"$log" 2>&1 &
  else
    "$@" >"$log" 2>&1 &
  fi
  LAUNCHED_PID=$!
}

wait_url() {
  local url="$1" pid="$2"
  for _ in $(seq 1 90); do
    kill -0 "$pid" 2>/dev/null || return 1
    if curl -fsS -o /dev/null -m 1 "$url" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

assert_port_free() {
  if curl -sS -o /dev/null -m 1 "$1" 2>/dev/null; then
    printf '%s\n' "A server already responds at $1; refusing to test or stop an unrelated process."
    return 1
  fi
}

# Read a field from OUR process's ready banner, never a user database/config.
# adminstack/devstack already print these test-only endpoints; no new entrypoint
# or production configuration is needed to share their PG and mock.
ready_field() {
  local file="$1" field="$2" label value rest
  while read -r label value rest; do
    if [ "$label" = "$field" ]; then
      printf '%s' "$value"
      return 0
    fi
  done <"$file"
  return 1
}

if [ "$EXTERNAL" != "1" ]; then
  # These existing test binaries use fixed HTTP addresses. Reject misleading
  # overrides rather than accidentally probing a different running stack.
  if [ "$ADMIN_URL" != "http://127.0.0.1:8090" ] ||
     [ "$GATEWAY_URL" != "http://127.0.0.1:12800" ] ||
     [ "$MOCK_CONTROL_URL" != "http://127.0.0.1:8091" ]; then
    printf '%s\n' 'Auto mode uses admin :8090, gateway :12800, mock stack :8080/:8091; use WEB_E2E_EXTERNAL=1 for other addresses.'
    exit 1
  fi
  assert_port_free "$ADMIN_URL/healthz" || exit 1
  assert_port_free "$GATEWAY_URL/healthz" || exit 1
  assert_port_free "http://127.0.0.1:8080/healthz" || exit 1
  assert_port_free "$MOCK_CONTROL_URL" || exit 1
  assert_port_free "$WEB_BASE_URL/login" || exit 1

  printf '%s\n' 'Building adminstack, mock-backed devstack and gateway...'
  ( cd "$ROOT" && go build -tags adminstack -o "$STACK_BIN" ./cmd/adminstack &&
    go build -tags devstack -o "$DEVSTACK_BIN" ./cmd/devstack &&
    go build -o "$GATEWAY_BIN" ./cmd/gateway ) >"$RUN_DIR/go-build.log" 2>&1 || {
    cat "$RUN_DIR/go-build.log"; exit 1;
  }
  # Explicitly override inherited DSN/persistence/demo flags. The test may not
  # connect to a user's development DB or seed real-provider demonstration data.
  launch "$RUN_DIR/adminstack.log" env GATEWAY_PG_DSN= GATEWAY_PERSIST_DATA=0 \
    GATEWAY_SEED_DEMO=0 TMPDIR="$RUN_DIR" "$STACK_BIN"
  STACK_PID="$LAUNCHED_PID"
  wait_url "$ADMIN_URL/healthz" "$STACK_PID" || { cat "$RUN_DIR/adminstack.log"; exit 1; }
  SHARED_DSN="$(ready_field "$RUN_DIR/adminstack.log" postgres)" || exit 1
  case "$SHARED_DSN" in
    postgres://postgres:postgres@localhost:*/voxeltoad_adminstack\?sslmode=disable) ;;
    *) printf '%s\n' 'Cannot identify the owned adminstack PG DSN.'; exit 1 ;;
  esac
  launch "$RUN_DIR/devstack.log" env GATEWAY_PG_DSN="$SHARED_DSN" \
    GATEWAY_ALLOW_INSECURE_DEV=1 TMPDIR="$RUN_DIR" "$DEVSTACK_BIN"
  DEVSTACK_PID="$LAUNCHED_PID"
  wait_url "http://127.0.0.1:8080/healthz" "$DEVSTACK_PID" || { cat "$RUN_DIR/devstack.log"; exit 1; }

  # Seed only the local mock into admin. The real gateway below consumes this
  # snapshot, so browser key/model/attribution flows use one consistent config.
  MOCK_URL="$(ready_field "$RUN_DIR/devstack.log" upstream)" || exit 1
  case "$MOCK_URL" in
    http://127.0.0.1:*) ;;
    *) printf '%s\n' 'Refusing a non-local mock upstream.'; exit 1 ;;
  esac
  ADMIN_TOKEN="$(curl -fsS "$ADMIN_URL/auth/login" -H 'Content-Type: application/json' \
    -d '{"email":"root@adminstack","password":"adminstack-pass-123"}' |
    node -e 'let s="";process.stdin.on("data",v=>s+=v);process.stdin.on("end",()=>{const t=JSON.parse(s).token;if(!t)process.exit(1);process.stdout.write(t)})')" || exit 1
  seed() {
    curl -fsS "$ADMIN_URL/api/v1/$1" -H "Authorization: Bearer $ADMIN_TOKEN" \
      -H 'Content-Type: application/json' -d "$2" > /dev/null
  }
  seed providers "{\"name\":\"mock-openai\",\"type\":\"openai\",\"endpoints\":[{\"id\":\"openai\",\"adapter\":\"openai\",\"base_url\":\"$MOCK_URL\"}],\"api_key_ref\":\"plain://sk-upstream\"}" || exit 1
  seed models '{"alias":"chat","upstreams":[{"provider":"mock-openai","upstream_model":"gpt-4o","pricing":{"prompt_per_1m":1000000,"completion_per_1m":2000000,"currency":"usd"}}]}' || exit 1
  seed routes '{"model_alias":"chat","strategy":"priority","providers":[{"name":"mock-openai"}]}' || exit 1
  # Capture is opt-in, including in tests; these payloads contain test prompts only.
  curl -fsS -X PUT "$ADMIN_URL/api/v1/gateway-settings" -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"trace":{"capture_payload_enabled":true,"max_body_kb":64,"retention_days":1}}' >/dev/null || exit 1
  node -e 'require("node:fs").writeFileSync(process.argv[1], JSON.stringify({gateway:{addr:"127.0.0.1:12800",allow_insecure_dev:true},snapshot:{admin_url:process.argv[3],poll_interval:"100ms"},db:{dsn:process.argv[2]},otel:{enabled:false}}))' \
    "$RUN_DIR/gateway.yaml" "$SHARED_DSN" "$ADMIN_URL" || exit 1
  launch "$RUN_DIR/gateway.log" env GATEWAY_CONFIG= GATEWAY_ALLOW_INSECURE_DEV=1 \
    "$GATEWAY_BIN" -config "$RUN_DIR/gateway.yaml"
  GATEWAY_PID="$LAUNCHED_PID"
  wait_url "$GATEWAY_URL/readyz" "$GATEWAY_PID" || { cat "$RUN_DIR/gateway.log"; exit 1; }

  printf '%s\n' 'Building SDK and Control Panel...'
  ( cd "$ROOT" && make sdk-build ) >"$RUN_DIR/sdk-build.log" 2>&1 || { cat "$RUN_DIR/sdk-build.log"; exit 1; }
  if [ ! -d "$WEB_DIR/node_modules" ]; then
    ( cd "$WEB_DIR" && npm install --silent ) || exit 1
  fi
  ( cd "$WEB_DIR" && npx playwright install chromium ) >"$RUN_DIR/playwright-install.log" 2>&1 || { cat "$RUN_DIR/playwright-install.log"; exit 1; }
  ( cd "$WEB_DIR" && ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" npm run build ) >"$RUN_DIR/web-build.log" 2>&1 || { cat "$RUN_DIR/web-build.log"; exit 1; }

  # Direct node PID rather than npm's wrapper, so macOS (without setsid) also
  # stops the actual server and leaves no orphan Next process.
  launch "$RUN_DIR/web.log" env ADMIN_URL="$ADMIN_URL" SESSION_SECRET="$SESSION_SECRET" \
    node -e 'process.chdir(process.argv[1]); process.argv=[process.argv[0],...process.argv.slice(2)]; require(process.argv[1]);' \
    "$WEB_DIR" "$WEB_DIR/node_modules/next/dist/bin/next" start -p "$WEB_PORT"
  WEB_PID="$LAUNCHED_PID"
  wait_url "$WEB_BASE_URL/login" "$WEB_PID" || { cat "$RUN_DIR/web.log"; exit 1; }
fi

printf '%s\n' "Running browser tests against admin + gateway; logs: $RUN_DIR"
WEB_BASE_URL="$WEB_BASE_URL" ADMIN_URL="$ADMIN_URL" \
GATEWAY_URL="$GATEWAY_URL" MOCK_CONTROL_URL="$MOCK_CONTROL_URL" \
VOXELTOAD_ADMIN_EMAIL="$ADMIN_EMAIL" VOXELTOAD_ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  npm --prefix "$WEB_DIR" run test:e2e -- "$@"
RESULT=$?
printf '%s\n' "Browser E2E exit status: $RESULT; logs: $RUN_DIR"
exit "$RESULT"
