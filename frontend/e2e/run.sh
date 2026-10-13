#!/usr/bin/env bash
# Builds the real gateway, starts it with two backends in front of a recording engine, starts the Vite dev
# server in real mode, and runs the browser test. Needs: Go, Node 20.19+/22.12+, and Chromium for Playwright
# (one-time: `npx playwright install chromium` inside this directory).
set -uo pipefail
cd "$(dirname "$0")"
HERE="$PWD"; FRONTEND="$HERE/.."; ROOT="$HERE/../.."
ENGINE_PORT="${ENGINE_PORT:-9101}"; GATEWAY_PORT="${GATEWAY_PORT:-18096}"; UI_PORT="${UI_PORT:-5173}"
mkdir -p out

node -e 'const [a,b]=process.versions.node.split(".").map(Number); process.exit((a>22||(a==22&&b>=12)||(a==20&&b>=19))?0:1)' \
  || { echo "Node $(node -v) is too old: Vite 8 needs 20.19+ or 22.12+" >&2; exit 2; }
[ -d node_modules/playwright ] || npm install --no-audit --no-fund >/dev/null || exit 2
[ -d "$FRONTEND/node_modules" ] || (cd "$FRONTEND" && npm ci --no-audit --no-fund >/dev/null) || exit 2

echo "building the gateway..."
(cd "$ROOT" && go build -o "$HERE/out/gateway" ./cmd/gateway) || exit 2

pids=()
cleanup() {
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null; done
  pkill -f "vite --port=$UI_PORT" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

ENGINE_PORT="$ENGINE_PORT" node engine.mjs > out/engine.log 2>&1 & pids+=($!)
sleep 1
BACKENDS=a,b \
  BACKEND_A_KIND=vllm BACKEND_A_URL="http://127.0.0.1:$ENGINE_PORT" BACKEND_A_MODEL=model-a \
  BACKEND_B_KIND=vllm BACKEND_B_URL="http://127.0.0.1:$ENGINE_PORT" BACKEND_B_MODEL=model-b \
  CORS_ALLOWED_ORIGINS="http://localhost:$UI_PORT" WS_ALLOWED_ORIGINS="localhost:$UI_PORT" \
  ROUTING_POLICY=round_robin LISTEN_ADDR=":$GATEWAY_PORT" LOG_LEVEL=warn \
  ./out/gateway > out/gateway.log 2>&1 & pids+=($!)
(cd "$FRONTEND" && VITE_MOCK=false VITE_GATEWAY_URL="http://localhost:$GATEWAY_PORT" \
  npx vite --port="$UI_PORT" --strictPort > "$HERE/out/vite.log" 2>&1) & pids+=($!)

# Wait for the UI and for ROUTING (the gateway's cached health) to see both backends healthy.
for _ in $(seq 1 40); do
  curl -s -m 1 "localhost:$UI_PORT" >/dev/null && \
  [ "$(curl -s -m 1 "localhost:$GATEWAY_PORT/v1/backends" | grep -o '"healthy":true' | wc -l)" -ge 2 ] && break
  sleep 1
done

UI_URL="http://localhost:$UI_PORT" ENGINE_PORT="$ENGINE_PORT" node test.mjs out
