# Benchmarking with inferbench

`cmd/inferbench` is a load generator for the gateway (or any OpenAI-compatible
`/v1/chat/completions` endpoint). It reports latency, TTFT and throughput as a terminal table
and a JSON file. It never fabricates numbers: every figure comes from a request it actually sent.

```bash
go build -o bin/inferbench ./cmd/inferbench

bin/inferbench --target http://localhost:8080 --concurrency 1,5,10,25,50 --duration 60s \
  --hardware "Apple M3 16GB" --engine vllm --model <model-name>

bin/inferbench --scenario benchmarks/scenarios/b-increasing-concurrency.json \
  --hardware "..." --engine "..." --model "..."

bin/inferbench compare vllm.json triton.json
```

## Flags

| Flag | Meaning | Default |
|---|---|---|
| `--target` | gateway base URL | `http://localhost:8080` |
| `--concurrency` | comma-separated levels, run one after another | `1` |
| `--duration` | how long each level keeps *starting* requests; in-flight ones finish and are counted | `60s` |
| `--warmup` | sent and discarded before each level | `0` |
| `--rate` | requests/s (open loop); `0` = closed loop | `0` |
| `--dataset` | JSONL (`{"prompt": ...}`) or one prompt per line; prompts are used round-robin in file order | one built-in prompt |
| `--stream` | streaming responses (enables TTFT) | off |
| `--max-tokens` / `--timeout` | request cap / per-request timeout | `128` / `2m` |
| `--model` | model name sent upstream; also recorded | none |
| `--hardware`, `--engine`, `--notes` | what the **server** runs on; recorded in the JSON | none |
| `--scenario` | scenario JSON; explicit flags override it | none |
| `--output` | results path | `benchmarks/results/<name>-<timestamp>.json` (git-ignored) |

## Load models

- **Closed loop** (default): `N` workers each send a request, wait for the reply, and send the next.
  Offered load drops automatically when the server slows down, so it measures capacity at a given
  concurrency but understates behaviour under overload.
- **Open loop** (`--rate R`): requests arrive on a fixed clock regardless of how fast replies come,
  with at most `--concurrency` in flight. Arrivals that find the cap reached are **dropped and
  counted** (`dropped_arrivals`), so an overloaded server shows up as drops, not as a hidden drop in
  offered load.

## What is measured

- **Latency**: request sent to response complete (whole stream for streaming), successful requests
  only. Reported as mean/min/max and p50/p95/p99 using the exact nearest-rank percentile over every
  sample (no sampling or histogram approximation).
- **TTFT** (streaming): request sent to the first chunk with non-empty content. The initial
  `role` chunk does not count.
- **req/s**: successful requests / wall-clock seconds from the first request to the last completion.
- **tok/s**: output tokens / the same wall-clock seconds, aggregate across all clients. Token counts
  are the server-reported `usage`. If a response has no usage it is counted in `missing_usage` and
  contributes zero tokens (the table warns that tok/s undercounts); tokens are never estimated.
- **Failures** are counted by class (`http_503`, `http_429`, `timeout`, `connection`,
  `stream_error`, `stream_truncated`, ...) and excluded from latency and TTFT.
- **Backend distribution**: from the gateway's `X-InferMesh-Backend` header.

## Reproducibility and honesty

The JSON records the full load configuration (levels, duration, rate, stream, max tokens,
dataset name and size), the client host, the operator-supplied `hardware` / `engine` / `model` /
`notes`, and snapshots of the gateway's `/v1/backends` before and after the run. The client
cannot see what the server runs on, which is why those three labels are flags: **always set them**
(missing ones print as `(unspecified)` and block comparison).

Things that affect results and are *not* controlled by the tool:

- Run the client on a different machine from the server when you can. On a single laptop the load
  generator and the model compete for CPU and memory bandwidth.
- Discard cold-start effects with `--warmup`, and repeat runs; a single run is one sample.
- Short runs give noisy p99 values. p99 from 20 requests is simply the slowest request.
- Closed-loop numbers at one concurrency are not a capacity limit.

## Scenarios (`benchmarks/scenarios/`)

| File | Question it answers |
|---|---|
| `a-low-concurrency.json` | Single-request latency with no queueing |
| `b-increasing-concurrency.json` | Where throughput saturates and latency climbs (1, 5, 10, 25, 50) |
| `c-long-prompts.json` | Effect of long prompts (prefill cost) on TTFT and latency, streaming |
| `d-short-prompts.json` | Same levels as C with short prompts, so only prompt length changes |
| `e-streaming.json` | TTFT and decode behaviour while streaming under rising concurrency |
| `f-backend-comparison.json` | Template for comparing two engines (see below) |

Datasets: `short.jsonl` (40 one-line questions) and `long.jsonl` (12 prompts of about 3,300
characters; regenerate deterministically with `python3 benchmarks/datasets/generate_long.py`).

## Comparing backends (experiment F)

Run the same scenario against each engine on comparable hardware with the same model, labelling each
run, then compare:

```bash
bin/inferbench --scenario benchmarks/scenarios/f-backend-comparison.json \
  --target http://gpu-box:8080 --hardware "1x A10G" --engine vllm --model <model> --output vllm.json
# ... same hardware and model, gateway pointed at Triton + TensorRT-LLM:
bin/inferbench --scenario benchmarks/scenarios/f-backend-comparison.json \
  --target http://gpu-box:8080 --hardware "1x A10G" --engine triton-trtllm --model <model> --output triton.json
bin/inferbench compare vllm.json triton.json
```

`compare` **refuses** (exit 1) unless both reports state the same hardware and model, name
different engines, and used the same stream mode, `max_tokens`, dataset, rate and concurrency
levels. `--force` prints the table anyway and says it is not a fair comparison.

## Status

The tool is verified against the local development stack (gateway + `mlx_lm.server` on Apple
Silicon). No benchmark results are published in this repository yet: real measurements, including the
vLLM vs Triton + TensorRT-LLM comparison, are recorded in `docs/performance.md` only after they have
been run on suitable hardware.
