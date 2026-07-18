#!/usr/bin/env bash
# Dev environment for the desktop personal gateway: Go gateway + Vite HMR.
#
# Starts the desktop gateway on 127.0.0.1:8787 (serving /v1/* data plane +
# /api/v1/* read API) in the background, then runs `npm run dev` in the
# foreground so you get hot-reloading desktop-ui against the real read API.
# The SPA's /api/v1 + /v1 fetches are proxied to the gateway via
# desktop-ui/vite.config.ts (server.proxy) — without that proxy the SPA at
# :5173 would 404 against the read API at :8787.
#
# Run:   ./scripts/desktop-web-dev.sh
# Env:   GATEWAY_PORT (default 8787)
#        GATEWAY_SEED_DEEPSEEK_KEY / GATEWAY_SEED_TOKENHUB_KEY /
#        GATEWAY_SEED_KIMI_KEY (optional real upstream keys; copy
#        .env.example → .env and fill in to enable real upstream calls)
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Load .env if present so developers don't have to export manually.
[ -f "$ROOT/.env" ] && set -a && . "$ROOT/.env" && set +a

GATEWAY_PORT="${GATEWAY_PORT:-8787}"
WORKDIR="$(mktemp -d)"
CFG_PATH="$WORKDIR/desktop.yaml"

green() { printf '\033[1;32m%s\033[0m' "$1"; }
hr()    { printf '\n\033[1;34m== %s ==\033[0m\n' "$1"; }

DESKTOP_PID=""
cleanup() {
  [ -n "$DESKTOP_PID" ] && kill -TERM "$DESKTOP_PID" 2>/dev/null
  wait "$DESKTOP_PID" 2>/dev/null || true
  rm -rf "$WORKDIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

hr "writing gateway config ($CFG_PATH)"
cat >"$CFG_PATH" <<EOF
# Desktop personal gateway — dev config (mirrors cmd/desktop/seed.ConfigTemplate).
# Real test providers (same as cmd/adminstack/seed.go). Edit to change routing.
gateway:
  addr: "127.0.0.1:$GATEWAY_PORT"
  session_headers: [X-Voxeltoad-Session]
providers:
  - name: 深度求索
    type: deepseek
    adapter: openai
    base_url: https://api.deepseek.com
    api_key_ref: "plain://${GATEWAY_SEED_DEEPSEEK_KEY:-}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
  - name: TokenHub
    type: tencent
    adapter: openai
    base_url: https://tokenhub.tencentmaas.com/v1
    api_key_ref: "plain://${GATEWAY_SEED_TOKENHUB_KEY:-}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
  - name: Kimi-code
    type: Kimi
    adapter: openai
    base_url: https://api.kimi.com/coding/v1
    api_key_ref: "plain://${GATEWAY_SEED_KIMI_KEY:-}"
    timeouts: {connect: 2s, first_byte: 5s, overall: 30s}
    weight: 100
models:
  - alias: deepseek-v4-flash
    upstreams:
      - {provider: 深度求索, upstream_model: deepseek-v4-flash, pricing: {prompt_per_1m: 2500000, completion_per_1m: 10000000, currency: usd}}
      - {provider: TokenHub, upstream_model: deepseek-v4-flash, pricing: {prompt_per_1m: 3000000, completion_per_1m: 15000000, currency: usd}}
  - alias: deepseek-v4-pro
    upstreams:
      - {provider: 深度求索, upstream_model: deepseek-v4-pro, pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}}
  - alias: hy3
    upstreams:
      - {provider: TokenHub, upstream_model: hy3, pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}}
  - alias: kimi-k2.7-code
    upstreams:
      - {provider: TokenHub, upstream_model: kimi-k2.7-code, pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}}
  - alias: kimi-for-coding
    upstreams:
      - {provider: Kimi-code, upstream_model: kimi-for-coding, pricing: {prompt_per_1m: 150000, completion_per_1m: 600000, currency: usd}}
routes:
  - {model_alias: deepseek-v4-flash, strategy: priority, providers: [{name: 深度求索, weight: 1}, {name: TokenHub, weight: 1}]}
  - {model_alias: deepseek-v4-pro, strategy: round_robin, providers: [{name: 深度求索, weight: 1}, {name: TokenHub, weight: 1}]}
  - {model_alias: hy3, strategy: session_affinity, providers: [{name: TokenHub, weight: 1}]}
  - {model_alias: kimi-k2.7-code, strategy: session_affinity, providers: [{name: TokenHub, weight: 1}]}
  - {model_alias: kimi-for-coding, strategy: session_affinity, providers: [{name: Kimi-code, weight: 1}]}
settings:
  trace: {capture_payload_enabled: true, max_body_kb: 256, retention_days: 30}
EOF
echo "  config at $CFG_PATH (mirrors seed.ConfigTemplate; edit to change providers)"

hr "starting desktop gateway on 127.0.0.1:$GATEWAY_PORT"
go run "$ROOT/cmd/desktop" -config "$CFG_PATH" -db "$WORKDIR/desktop.db" >"$WORKDIR/desktop.log" 2>&1 &
DESKTOP_PID=$!

# Wait for the gateway health endpoint.
for _ in $(seq 1 40); do
  if curl -sS -o /dev/null -m 1 "http://127.0.0.1:$GATEWAY_PORT/api/v1/health" 2>/dev/null; then break; fi
  sleep 0.5
done
if ! curl -sS -o /dev/null -m 1 "http://127.0.0.1:$GATEWAY_PORT/api/v1/health" 2>/dev/null; then
  echo "desktop gateway did not start. log:"; cat "$WORKDIR/desktop.log"; exit 1
fi
echo "  $(green 'gateway up') — api key: desktop-local-default-key"
echo "  gateway log: $WORKDIR/desktop.log"

hr "starting Vite dev server (desktop-ui)"
echo "  SPA will proxy /api/v1/* and /v1/* to http://127.0.0.1:$GATEWAY_PORT"
echo "  open the URL Vite prints below (usually http://127.0.0.1:5173)."
echo "  Ctrl-C stops both processes."
echo
cd "$ROOT/desktop-ui" || { echo "desktop-ui/ missing"; exit 1; }
if [ ! -d node_modules ]; then
  echo "  node_modules missing, running npm install..."
  npm install || { echo "npm install failed"; exit 1; }
fi
exec npm run dev -- --host 127.0.0.1
