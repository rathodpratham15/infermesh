# Performance

> **Read this first.** Everything below was measured on **one Apple M3 laptop** (client, gateway and engine
> all on the same machine) against **mlx-lm serving a 0.5B-parameter 4-bit model**, which is a development
> stand-in for vLLM. **It is not vLLM, not Triton, not TensorRT-LLM and not a GPU.** These numbers document
> how the InferMesh gateway and `inferbench` behave and what was learned building them. They say **nothing**
> about how vLLM or Triton + TensorRT-LLM perform, and must not be quoted as such. The vLLM vs Triton
> comparison (experiment F) has **not been run**; see the last section.

All tables are generated from the raw JSON in `benchmarks/recorded/2026-10-local-m3/` by
`benchmarks/tools/results_to_md.py`; nothing is transcribed by hand. Each JSON records its own hardware,
engine, model and load configuration.

## Environment and method

| | |
|---|---|
| Machine | Apple M3 (8 cores), 16 GB, macOS 27.0.1. Client, gateway and engine share it, so they compete for CPU and memory bandwidth |
| Engine | `mlx_lm.server` 0.32.0 (mlx 0.32.3), OpenAI-compatible; **not vLLM** |
| Model | `mlx-community/Qwen2.5-0.5B-Instruct-4bit` |
| Gateway | commit `11edf6d`, `least_outstanding` routing, **1 backend**, at most **6** concurrent requests to the engine, queue of 128 |
| Load | `inferbench`, closed loop, 60 s per level after a 10 s discarded warmup, `max_tokens` 128 (64 for the overload check) |
| Datasets | `short.jsonl` (40 one-line questions); `long.jsonl` (12 prompts of about 3,300 characters) |
| Power | The suite **started on battery power (69%)**. The Mac was plugged in at some point during the run (the exact time was not recorded); the later paired runs were on AC power |

**Why the gateway limit is 6.** `mlx_lm.server` is built on Python's `http.server` and resets connections when
many arrive at once. Probed directly (6 s runs, streaming and non-streaming), it had 0 failures up to 6
concurrent clients and failures from 8 upward. The limit was set to the last safe value so that higher client
concurrency queues in the gateway. This is a property of this stand-in engine: real engines batch many requests
and do not have this limit, so **do not carry the number 6 over to vLLM or Triton**.

### What is measured

Latency is request-sent to response-complete (the whole stream when streaming), successful requests only. TTFT is
request-sent to the first content chunk and, because requests queue in the gateway, **includes queue wait**.
Percentiles are exact nearest-rank. `req/s` and `tok/s` divide by wall-clock time **including the drain of
in-flight requests after the load window**, which understates steady-state throughput at high concurrency (see
Caveats). Token counts are server-reported usage.

## Result quality: validity checks

- **Little's law holds.** For a closed loop, in-flight requests should equal `req/s x mean latency`. Across every
  level the ratio to the configured client count was 0.89 to 1.00 (it falls below 1 at high concurrency because
  requests drain after the window). So the measurements are internally consistent.
- **Run-to-run variance (same settings, back to back).** Three repeats of experiment A:

| conc | metric | run 1 | run 2 | run 3 | mean | max-min |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | req/s | 0.92 | 0.91 | 0.89 | 0.91 | 0.02 |
| 1 | tok/s | 108.5 | 107.5 | 105.5 | 107.2 | 3.0 |
| 1 | p50 ms | 1169 | 1155 | 1140 | 1155 | 29 |

  `req/s` and p50 agree within about 3%, but **p99 does not**: p99 was 1657, 1531 and 2458 ms across three
  identical runs. With about 55 requests per run, p99 is essentially the single slowest request. Treat p99 in
  60-second runs as noisy.
- **Drift over time.** The same `c=1` non-streaming short-prompt workload gave 0.89 to 0.92 req/s in the first runs
  (A repeats, B) and again in the later paired runs (0.76 to 0.92), but only **0.66 req/s** (gateway) and **0.51 req/s**
  (direct) late in the main suite (the `g-overhead` runs). Throughput dips of 25 to 45% appeared and recovered. The
  cause was not determined: candidates are the change from battery to AC power, thermal behaviour under sustained load,
  and residual engine state after the heaviest experiments. **Consequently, compare trends within one experiment,
  not absolute values across experiments, and treat C, D, E and the G runs as measured during a noisier period.**

## Experiment A: low concurrency (see variance table above)

One client, short prompts, non-streaming: about 0.9 req/s and about 105 output tokens/s, p50 about 1.15 s.
This is the single-request latency of the engine plus the gateway.

## Experiment B: increasing concurrency

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 54 | 0 | 0.89 | 104.7 | 1206 | 1412 | 1651 |
| 5 | 187 | 0 | 3.07 | 360.0 | 1618 | 2297 | 2710 |
| 10 | 204 | 1 | 3.25 | 381.5 | 3020 | 5281 | 6566 |
| 25 | 194 | 1 | 2.77 | 323.7 | 6862 | 19920 | 28370 |
| 50 | 211 | 0 | 2.60 | 306.9 | 13151 | 41014 | 60357 |

Throughput rises from 0.89 req/s at 1 client to about 3.1 to 3.3 req/s at 5 to 10 clients, then **stops
improving and slightly declines** (2.77 at 25, 2.60 at 50) while latency keeps growing: p50 reaches 13 s at 50
clients. That is the signature of saturation: the engine is the bottleneck, the gateway admits 6 at a time, and
the rest wait in its queue (p99 about 60 s at 50 clients). The small decline at 25 and 50 clients is at least partly
a measurement artifact: wall time was 81 s at 50 clients because requests queued behind the limit had to drain
(Caveats), and part of it may be the noise discussed above. This experiment shows where *this* engine saturates;
it is not a capacity figure for anything else.

## Experiments C and D: long vs short prompts (streaming)

Long prompts (C):

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 42 | 0 | 0.70 | 89.5 | 1232 | 2542 | 2783 | 329 | 1028 | 1251 |
| 4 | 72 | 0 | 1.20 | 153.1 | 3380 | 4431 | 4502 | 1829 | 2556 | 2562 |
| 8 | 68 | 0 | 1.07 | 137.4 | 6702 | 16537 | 22409 | 1683 | 9339 | 18937 |

Short prompts (D), same levels and settings:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 44 | 0 | 0.73 | 85.5 | 1219 | 2334 | 2627 | 54 | 134 | 303 |
| 4 | 84 | 0 | 1.36 | 160.1 | 2683 | 6805 | 8456 | 282 | 1647 | 2630 |
| 8 | 101 | 1 | 1.58 | 188.7 | 4417 | 10599 | 12568 | 659 | 6162 | 7767 |

The clearest effect is on **time to first token**. At one client, TTFT p50 was 329 ms with long prompts against
54 ms with short ones, a large multiple and far bigger than the roughly 30% drift seen elsewhere, so the direction and
rough size are credible (longer prompts cost more prefill before the first token). The throughput effect is smaller and
less certain: at 1 client the two are similar (0.70 vs 0.73 req/s, within noise), and the gap widens with concurrency
(1.07 vs 1.58 req/s at 8 clients), but C ran before D, so the drift above can account for part of it.
At 4 and 8 clients TTFT is dominated by queueing in the gateway rather than prefill.

## Experiment E: streaming under rising concurrency

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 31 | 0 | 0.50 | 61.1 | 1647 | 4317 | 4570 | 58 | 139 | 206 |
| 5 | 67 | 0 | 1.07 | 129.2 | 4374 | 6611 | 6717 | 323 | 1427 | 1456 |
| 10 | 89 | 0 | 1.29 | 152.5 | 6724 | 14620 | 35437 | 1936 | 10332 | 33049 |
| 25 | 110 | 0 | 1.59 | 185.0 | 11280 | 38033 | 42910 | 7536 | 33485 | 39334 |

TTFT grows with concurrency (p50 58 ms at 1 client, 7.5 s at 25) because TTFT includes the gateway queue wait once
more than 6 requests are in flight. Zero failures at every level, including 25 clients, because the gateway's limit keeps the
engine within its safe range.

## Overload protection: the same overload, direct vs through the gateway

16 streaming clients (`max_tokens` 64), straight at the engine and through the gateway:

Direct to the engine:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 16 | 321 | 64 | 4.15 | 0.0 | 3554 | 6351 | 6819 | 547 | 1296 | 1335 |

Through the gateway:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 16 | 203 | 0 | 3.01 | 184.0 | 3724 | 13084 | 17471 | 2025 | 11492 | 15872 |

Direct to the engine, **64 of 321 requests (20%) failed** with connection resets. Through the gateway, **0 of 203 failed**:
the gateway's limit and queue turned engine-side failures into waiting. The cost is visible too: successful throughput
was lower (3.01 vs 4.15 req/s) and tail latency higher (p95 13.1 s vs 6.4 s, p99 17.5 s vs 6.8 s, because requests queue). Two caveats on reading it:
(1) the direct latency figures cover only the requests that **succeeded**, so they are biased toward the lucky ones;
(2) `tok/s` is 0 for the direct run because the engine returns no usage in streams unless the client asks for it with
`stream_options`, which `inferbench` does not send, while the gateway asks on its behalf. The limit of 6 was the last value with no failures in the probe (6 s runs; 7 was not tested, 8 failed), so a slightly higher limit might recover some throughput.

## Gateway overhead

The question: how much latency does the gateway add over calling the engine directly?

A single direct run followed by a single gateway run (from the main suite) is **inconclusive**, because of the drift described
above; it even shows the gateway as faster:

| conc | metric | direct to engine | via gateway | difference | relative |
|---:|---:|---:|---:|---:|---:|
| 1 | p50 latency ms | 1583 | 1346 | -237 | -15.0% |
| 1 | p95 latency ms | 4123 | 2832 | -1291 | -31.3% |
| 1 | p99 latency ms | 5142 | 3291 | -1851 | -36.0% |
| 1 | req/s | 0.51 | 0.66 | 0.15 | +28.7% |
| 4 | p50 latency ms | 2965 | 2842 | -122 | -4.1% |
| 4 | p95 latency ms | 5815 | 4724 | -1091 | -18.8% |
| 4 | p99 latency ms | 8384 | 4870 | -3514 | -41.9% |
| 4 | req/s | 1.39 | 1.38 | -0.01 | -0.8% |

To control for drift, the runs were repeated as **five pairs, alternating direct and gateway** (swapping which goes first
on every other pair), 20 s per level, on AC power:

**concurrency 1**: per-pair results (direct to engine vs via gateway)

| pair | direct p50 ms | gateway p50 ms | p50 diff ms | direct req/s | gateway req/s | req/s diff | failed |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 1226 | 1156 | -71 | 0.90 | 0.90 | -0.4% | 0 |
| 2 | 1120 | 1127 | +7 | 0.93 | 0.92 | -1.4% | 0 |
| 3 | 1184 | 1297 | +113 | 0.86 | 0.76 | -11.1% | 0 |
| 4 | 1252 | 1222 | -30 | 0.81 | 0.88 | +7.8% | 0 |
| 5 | 1180 | 1131 | -49 | 0.92 | 0.91 | -0.8% | 0 |

p50 difference (gateway - direct): median -30 ms, min -71, max +113 over 5 pairs; req/s difference: median -0.8%, min -11.1%, max +7.8%

**concurrency 4**: per-pair results (direct to engine vs via gateway)

| pair | direct p50 ms | gateway p50 ms | p50 diff ms | direct req/s | gateway req/s | req/s diff | failed |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 1503 | 1473 | -30 | 2.63 | 2.73 | +3.5% | 0 |
| 2 | 2075 | 1412 | -663 | 2.03 | 2.65 | +30.9% | 0 |
| 3 | 1958 | 2344 | +386 | 2.05 | 1.79 | -12.6% | 0 |
| 4 | 1909 | 1785 | -124 | 2.04 | 1.89 | -7.2% | 0 |
| 5 | 2008 | 1967 | -41 | 2.06 | 2.12 | +2.6% | 0 |

p50 difference (gateway - direct): median -41 ms, min -663, max +386 over 5 pairs; req/s difference: median +2.6%, min -12.6%, max +30.9%

**Conclusion: the gateway's overhead is below the noise floor of this setup.** The median difference was about -30 ms
(concurrency 1) and -41 ms (concurrency 4) with individual pairs swinging by 100 to 660 ms in both directions, which is
the engine's run-to-run variability, not proxy cost. Accordingly: at concurrency 1 the pair-to-pair noise is about 100 ms (-71 to +113 ms), roughly 8% of a 1.2 s request, so any
gateway overhead smaller than that **cannot be detected** here. This does **not** show that the overhead is zero, and it does not give a number. Measuring a proxy's cost properly
needs a fast, low-variance backend (for example a no-op engine) and a load generator on a different machine. That was not done.

## Experiment F: vLLM vs Triton + TensorRT-LLM: **not run**

This comparison needs an NVIDIA GPU machine, and this project was developed on an Apple Silicon laptop, so there are **no
vLLM, Triton or TensorRT-LLM measurements in this repository and none are claimed.** The pieces exist and are tested
without a GPU (the Triton backend against a fake server, the repository checker, the report-comparison tool that refuses
unlike-for-like runs), but the engines themselves have never been run here.

| Engine | Hardware | Model | Result |
|---|---|---|---|
| vLLM | not run | not run | not run |
| Triton + TensorRT-LLM | not run | not run | not run |

To run it, follow the procedure in `gpu-deployment.md`: same GPU machine, same model, one engine behind the gateway
at a time, `inferbench --scenario benchmarks/scenarios/f-backend-comparison.json` with `--engine`, `--hardware` and
`--model` set, then `inferbench compare`. Record the engine versions and the precision, batching and engine-build
settings alongside the results.

## Problems found while benchmarking

- **A single backend cannot retry a transient failure.** Three requests in the main suite (at 10 and 25 clients in B, 8 in
  D) failed with HTTP 502 after the engine reset a connection even within the safe limit (4 such errors in the gateway log,
  3 inside measured windows, 3 of 2,050 gateway requests, 0.15%). The gateway retries only on a *different* backend, so with one backend
  a connection reset is never retried. Retrying once on the same backend for connection-level errors would likely have
  absorbed these; it is not implemented. The 10 paired runs at 1 and 4 clients had no gateway errors.
- **`inferbench` does not request token usage in streams**, so direct-to-engine streaming runs report no tokens/s unless the
  engine includes usage by default. Adding a `stream_options` flag would fix it.
- **Throughput includes drain time.** At 50 clients the wall time was 81 s for a 60 s window, so `req/s` is understated; a
  steady-state window (excluding drain) would be fairer.

## Reproducing

```bash
make local-model                      # or any OpenAI-compatible engine; see local-development.md
benchmarks/run_local.sh               # main suite, ~30 minutes; plug in the charger and quit heavy apps first
SUITE=overhead DURATION=20s WARMUP=3s benchmarks/run_local.sh    # paired overhead runs, ~8 minutes
python3 benchmarks/tools/results_to_md.py table benchmarks/results/<run>/b-increasing-concurrency.json
```

The main suite in this document was run before the `SUITE=overhead` option was added to the script (the commit hash in
`environment.txt` is the one the run started from, with a dirty tree); the rest of the script is unchanged.
