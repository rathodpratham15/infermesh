#!/usr/bin/env bash
# Runs the local benchmark suite (experiments A-E and an overload check; the paired gateway-overhead
# runs are SUITE=overhead) on one machine:
# the load generator, the gateway and the engine all share it. The engine is mlx-lm serving a small
# model on Apple Silicon: a development stand-in for vLLM, NOT vLLM and NOT a GPU. Results from this
# script must never be presented as vLLM or Triton + TensorRT-LLM performance.
#
#   benchmarks/run_local.sh                     # full suite, 60s per level (about 25 minutes)
#   DURATION=5s WARMUP=1s benchmarks/run_local.sh   # quick smoke test
#   SUITE=overhead DURATION=20s WARMUP=3s benchmarks/run_local.sh   # paired gateway-overhead runs
#
# Environment overrides: OUT, DURATION, WARMUP, MODEL, MLX_PY (python with mlx-lm installed),
# ALLOW_BATTERY=1 (run on battery power anyway; the results are then NOT comparable).
# For meaningful numbers: plug in the charger and close other heavy applications first.
# The script refuses to start on battery power, keeps the machine awake, and logs the power source
# before and after every run to $OUT/power.log, so a power change mid-suite is visible afterwards.
set -euo pipefail
cd "$(dirname "$0")/.."

DURATION="${DURATION:-60s}"
WARMUP="${WARMUP:-10s}"
MODEL="${MODEL:-mlx-community/Qwen2.5-0.5B-Instruct-4bit}"
MLX_PY="${MLX_PY:-.venv-local/bin/python}"
OUT="${OUT:-benchmarks/results/$(date +%Y%m%d-%H%M%S)-local}"
ENGINE_PORT=8000
GW_PORT=18090
# mlx_lm.server is built on Python's http.server and resets connections when many arrive at once.
# Probed directly (6s runs, streaming and non-streaming): 0 failures up to 6 concurrent clients,
# failures from 8 upward. The gateway's per-backend limit is set to the last safe value, so higher
# client concurrency queues in the gateway instead of overwhelming the engine. Real engines (vLLM,
# Triton) do not have this limit; do not carry this number over to them.
ENGINE_SAFE_CONCURRENCY=6
BIN=bin
mkdir -p "$OUT" "$BIN"

if pmset -g batt | grep -q "Battery Power" && [ "${ALLOW_BATTERY:-0}" != 1 ]; then
  echo "refusing to run on battery power: results would not be comparable. Plug in, or set ALLOW_BATTERY=1." >&2
  exit 1
fi
caffeinate -i -w $$ &   # keep the machine from idle-sleeping while this script runs

log_power() { echo "$(date -u +%FT%TZ) $1 | $(pmset -g batt | sed -n '1p;2p' | tr '\n\t' '  ')" >> "$OUT/power.log"; }

pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

echo "building binaries..."
go build -o "$BIN/inferbench" ./cmd/inferbench
go build -o "$BIN/gateway" ./cmd/gateway

echo "starting engine (mlx_lm.server, $MODEL)..."
"$MLX_PY" -m mlx_lm.server --model "$MODEL" --port "$ENGINE_PORT" >"$OUT/engine.log" 2>&1 &
pids+=($!)
for _ in $(seq 1 90); do curl -s -m 2 "localhost:$ENGINE_PORT/v1/models" >/dev/null && break; sleep 2; done
curl -s -m 2 "localhost:$ENGINE_PORT/v1/models" >/dev/null || { echo "engine did not start" >&2; exit 1; }

echo "starting gateway..."
BACKENDS=vllm BACKEND_VLLM_URL="http://127.0.0.1:$ENGINE_PORT" BACKEND_VLLM_MODEL="$MODEL" \
  BACKEND_VLLM_MAX_CONCURRENT="$ENGINE_SAFE_CONCURRENCY" ROUTING_POLICY=least_outstanding LISTEN_ADDR=":$GW_PORT" LOG_LEVEL=error \
  "$BIN/gateway" >"$OUT/gateway.log" 2>&1 &
pids+=($!)
for _ in $(seq 1 30); do curl -s -m 2 "localhost:$GW_PORT/readyz" >/dev/null && break; sleep 1; done

{
  echo "date:      $(date -u +%FT%TZ)"
  echo "git:       $(git rev-parse --short HEAD) $(git diff --quiet || echo '(dirty tree)')"
  echo "chip:      $(sysctl -n machdep.cpu.brand_string)"
  echo "cpus:      $(sysctl -n hw.ncpu)"
  echo "memory:    $(( $(sysctl -n hw.memsize) / 1073741824 )) GB"
  echo "os:        macOS $(sw_vers -productVersion)"
  echo "power:     $(pmset -g batt | sed -n '1p;2p' | tr '\n' ' ')"
  echo "lowpower:  $(pmset -g | grep -i lowpowermode | tr -s ' ')"
  echo "mlx-lm:    $("$MLX_PY" -m pip list 2>/dev/null | grep -E '^(mlx|mlx-lm) ' | tr -s ' ' | tr '\n' ',')"
  echo "model:     $MODEL"
  echo "go:        $(go version)"
  echo "gateway:   least_outstanding routing, max $ENGINE_SAFE_CONCURRENCY concurrent to the engine (queue 128), 1 backend"
  echo "duration:  $DURATION per level, warmup $WARMUP"
} | tee "$OUT/environment.txt"

HW="Apple M3 8-core, 16 GB, macOS $(sw_vers -productVersion); client, gateway and engine share this machine"
ENG="mlx-lm $("$MLX_PY" -c 'import mlx_lm; print(mlx_lm.__version__)') (OpenAI-compatible stand-in; not vLLM)"
common=(--hardware "$HW" --engine "$ENG" --model "$MODEL" --duration "$DURATION" --warmup "$WARMUP")
GW="http://localhost:$GW_PORT"
ENGINE="http://localhost:$ENGINE_PORT"

run() { # name target [extra flags...]
  local name="$1" target="$2"; shift 2
  echo; echo "=== $name"
  log_power "start $name"
  "$BIN/inferbench" "${common[@]}" --target "$target" --output "$OUT/$name.json" "$@" 2>/dev/null
  log_power "end   $name"
}

# SUITE=overhead: gateway overhead measured with PAIRED, ALTERNATING runs. Throughput on a laptop drifts by
# tens of percent over half an hour, so a single direct-vs-gateway pair cannot resolve a small difference.
# Alternating direct and gateway runs (and swapping which goes first on every other pair) lets slow drift
# cancel out; the per-pair differences are then summarized by results_to_md.py.
if [ "${SUITE:-main}" = overhead ]; then
  PAIRS="${PAIRS:-5}"
  for i in $(seq 1 "$PAIRS"); do
    if [ $((i % 2)) -eq 1 ]; then order=(direct gateway); else order=(gateway direct); fi
    for t in "${order[@]}"; do
      target="$GW"; [ "$t" = direct ] && target="$ENGINE"
      run "overhead-pair$i-$t" "$target" --concurrency 1,4 --dataset benchmarks/datasets/short.jsonl --max-tokens 128 \
        --notes "paired overhead run: pair $i, $t (order: ${order[*]})"
    done
  done
  echo; echo "done. results in $OUT"
  exit 0
fi

# A is repeated three times to show run-to-run variance.
for i in 1 2 3; do run "a-low-concurrency-run$i" "$GW" --scenario benchmarks/scenarios/a-low-concurrency.json --notes "repeat $i of 3"; done
run b-increasing-concurrency "$GW" --scenario benchmarks/scenarios/b-increasing-concurrency.json
run c-long-prompts           "$GW" --scenario benchmarks/scenarios/c-long-prompts.json
run d-short-prompts          "$GW" --scenario benchmarks/scenarios/d-short-prompts.json
run e-streaming              "$GW" --scenario benchmarks/scenarios/e-streaming.json

# Gateway overhead is measured by the paired suite (SUITE=overhead), not here: a single
# direct-vs-gateway pair cannot resolve a small difference under throughput drift.

# Protection: the same overload sent straight at the fragile engine and through the gateway's limiter.
for target_name in direct gateway; do
  target="$GW"; [ "$target_name" = direct ] && target="$ENGINE"
  run "g-overload-$target_name" "$target" --concurrency 16 --stream --dataset benchmarks/datasets/short.jsonl --max-tokens 64 \
    --notes "overload check at 16 clients: $target_name"
done

echo; echo "done. results in $OUT"
