# Performance

> **Read this first.** Everything below was measured on **one Apple M3 laptop** (client, gateway and engine
> all on the same machine) against **mlx-lm serving a 0.5B-parameter 4-bit model**, which is a development
> stand-in for vLLM. **It is not vLLM, not Triton, not TensorRT-LLM and not a GPU.** These numbers document
> how the InferMesh gateway and `inferbench` behave and what was learned building them. They say **nothing**
> about how vLLM or Triton + TensorRT-LLM perform, and must not be quoted as such. The vLLM vs Triton
> comparison (experiment F) has **not been run**; see the last section.

All tables are generated from the raw JSON in `benchmarks/recorded/` by `benchmarks/tools/results_to_md.py`;
nothing is transcribed by hand. The **primary results are run 2** (`2026-10-local-m3-run2/`). Run 1
(`2026-10-local-m3/`) was the first attempt, started on battery power; it is kept for transparency and as a
cross-check, and parts of it are known to be unreliable (see "Two runs compared").

## Environment and method (run 2)

| | |
|---|---|
| Machine | Apple M3 (8 cores), 16 GB, macOS 27.0.1. Client, gateway and engine share it, so they compete for CPU and memory bandwidth |
| Engine | `mlx_lm.server` 0.32.0 (mlx 0.32.3), OpenAI-compatible; **not vLLM** |
| Model | `mlx-community/Qwen2.5-0.5B-Instruct-4bit` |
| Gateway | `least_outstanding` routing, **1 backend**, at most **6** concurrent requests to the engine, queue of 128. The Go code is identical to commit `eb1f7fa` of the PR that adds this report (the run recorded `f049838`, its pre-cherry-pick hash, with a clean tree) |
| Load | `inferbench`, closed loop, 60 s per level after a 10 s discarded warmup (paired runs: 20 s after 3 s), `max_tokens` 128 (64 for the overload check) |
| Datasets | `short.jsonl` (40 one-line questions); `long.jsonl` (12 prompts of about 3,300 characters) |
| Power | **AC power for the whole run**: all 38 power-log entries (before and after every run) show AC, battery 96-99%, Low Power Mode off, and the script keeps the machine awake. It refuses to start on battery |

**Why the gateway limit is 6.** `mlx_lm.server` is built on Python's `http.server` and resets connections when
many arrive at once. Probed directly (6 s runs, streaming and non-streaming), it had 0 failures up to 6
concurrent clients and failures from 8 upward (7 was not tested). The limit is the last safe value, so higher
client concurrency queues in the gateway. This is a property of this stand-in engine: real engines batch many
requests and do not have it, so **do not carry the number 6 over to vLLM or Triton**.

### What is measured

- **Latency** is request-sent to response-complete (the whole stream when streaming), successful requests
  only, over every request that started during the load window. Percentiles are exact nearest-rank.
- **TTFT** is request-sent to the first content chunk. Because requests queue in the gateway, **it includes queue wait**.
- **req/s and tok/s** are *steady-state*: completions inside the load window after its first 20%, divided by that
  interval (excludes ramp-up and the drain of in-flight requests). The tables also show the older wall-clock
  `req/s` (all successes over total wall time). Token counts are server-reported usage.

## Validity checks

- **Little's law holds.** In a closed loop, in-flight requests should equal `req/s x mean latency`. In
  experiments B to E the ratio to the configured client count was 0.89 to 1.00 at every level (it falls below 1 at the highest
  concurrency because requests drain after the window).
- **Steady-state vs wall-clock throughput barely differ.** Across all 20 levels the difference ranges from
  -8.8% to +8.7% (mean +0.4%). The worry that drain time makes the old wall-clock figure understate throughput was
  **largely unfounded**: the steady-state definition is cleaner and more robust, but it did not change any conclusion.

| experiment | conc | steady req/s | wall req/s | drain s | steady vs wall |
|---:|---:|---:|---:|---:|---:|
| a-low-concurrency-run1 | 1 | 0.88 | 0.87 | 0.6 | +0.0% |
| a-low-concurrency-run2 | 1 | 0.92 | 0.93 | 0.3 | -1.3% |
| a-low-concurrency-run3 | 1 | 0.94 | 0.92 | 0.7 | +1.6% |
| b-increasing-concurrency | 1 | 0.90 | 0.88 | 1.1 | +1.4% |
| b-increasing-concurrency | 5 | 2.65 | 2.77 | 1.4 | -4.5% |
| b-increasing-concurrency | 10 | 3.23 | 3.29 | 2.3 | -1.8% |
| b-increasing-concurrency | 25 | 2.77 | 2.55 | 14.2 | +8.7% |
| b-increasing-concurrency | 50 | 2.88 | 2.77 | 18.7 | +3.8% |
| c-long-prompts | 1 | 0.62 | 0.64 | 0.7 | -2.8% |
| c-long-prompts | 4 | 1.42 | 1.41 | 2.5 | +0.6% |
| c-long-prompts | 8 | 1.25 | 1.37 | 2.8 | -8.8% |
| d-short-prompts | 1 | 0.58 | 0.56 | 0.9 | +4.4% |
| d-short-prompts | 4 | 1.85 | 1.88 | 3.3 | -1.4% |
| d-short-prompts | 8 | 2.21 | 2.24 | 2.4 | -1.6% |
| e-streaming | 1 | 0.85 | 0.85 | 0.3 | +1.0% |
| e-streaming | 5 | 2.52 | 2.41 | 1.9 | +4.7% |
| e-streaming | 10 | 2.88 | 2.87 | 2.0 | +0.1% |
| e-streaming | 25 | 2.54 | 2.61 | 9.5 | -2.4% |
| g-overload-direct | 16 | 7.40 | 7.48 | 1.7 | -1.1% |
| g-overload-gateway | 16 | 4.65 | 4.30 | 5.3 | +7.9% |

range of the difference: -8.8% to +8.7%, mean +0.4%

- **Run-to-run variance (same settings, back to back).** Three repeats of experiment A:

| conc | metric | run 1 | run 2 | run 3 | mean | max-min |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | req/s | 0.88 | 0.92 | 0.94 | 0.91 | 0.06 |
| 1 | tok/s | 102.6 | 107.9 | 110.6 | 107.0 | 8.0 |
| 1 | p50 ms | 1180 | 1120 | 1163 | 1154 | 61 |

  Throughput is within about 7% across repeats and p50 within about 5%; p99 was 1722, 1541 and 1588 ms.
  With about 55 requests per run, p99 is essentially the slowest request, so treat p99 from 60-second runs as noisy.
- **Intermittent stalls.** Even on AC power with a clean tree, a few single requests take several seconds longer than
  the rest. In experiment D at one client the mean latency was 1790 ms against a 1422 ms
  median, the slowest request took 6404 ms, and about 12.5 s of the 61 s window was lost to
  slow requests (the same workload in experiment E ran at 0.85 req/s against D's 0.58). With only about
  34 requests per level, a couple of stalls move req/s and p95/p99 a lot. The cause was not determined; the client, gateway and
  engine share one laptop with other processes running.

## Two runs compared

Run 1 predates the fixes in this PR (older `inferbench` and no same-backend retry), but the experiments are otherwise the
same. The table compares the wall-clock `req/s`, which both runs have:

| exp | conc | run 1 req/s (wall) | run 2 req/s (wall) | change | run 1 p50 ms | run 2 p50 ms | run 1 TTFT p50 | run 2 TTFT p50 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| B | 1 | 0.89 | 0.88 | -0% | 1206 | 1185 | - | - |
| B | 5 | 3.07 | 2.77 | -10% | 1618 | 1721 | - | - |
| B | 10 | 3.25 | 3.29 | +1% | 3020 | 2997 | - | - |
| B | 25 | 2.77 | 2.55 | -8% | 6862 | 7113 | - | - |
| B | 50 | 2.60 | 2.77 | +6% | 13151 | 13404 | - | - |
| C | 1 | 0.70 | 0.64 | -8% | 1232 | 1298 | 329 | 334 |
| C | 4 | 1.20 | 1.41 | +18% | 3380 | 2508 | 1829 | 842 |
| C | 8 | 1.07 | 1.37 | +28% | 6702 | 5806 | 1683 | 1547 |
| D | 1 | 0.73 | 0.56 | -24% | 1219 | 1422 | 54 | 53 |
| D | 4 | 1.36 | 1.88 | +38% | 2683 | 1786 | 282 | 274 |
| D | 8 | 1.58 | 2.24 | +42% | 4417 | 3084 | 659 | 821 |
| E | 1 | 0.50 | 0.85 | +71% | 1647 | 1224 | 58 | 51 |
| E | 5 | 1.07 | 2.41 | +124% | 4374 | 2062 | 323 | 236 |
| E | 10 | 1.29 | 2.87 | +123% | 6724 | 3165 | 1936 | 1290 |
| E | 25 | 1.59 | 2.61 | +64% | 11280 | 6976 | 7536 | 4647 |

- **Experiment B (increasing concurrency) reproduced** to within 10% at every level, and **TTFT at one client reproduced**
  almost exactly (long prompts 329 vs 334 ms, short prompts 54 vs 53 ms).
- **Run 1's results for C, D and E were far slower than run 2's**, by up to 2x (E at 5 clients: 1.07 vs 2.41 req/s). Run 1
  started on battery (69%) and the charger was plugged in at a point that was not recorded; when checked during the later
  experiments the battery was charging at about 70%. In run 2 the battery was at 96-99%. Fast charging under sustained load generating heat
  and throttling the chip is a plausible explanation, but **it was not measured**, and run 2 still shows the occasional
  stall described above. Run 1's C, D and E numbers should not be used as absolute figures.

## Experiments

### A: low concurrency

One client, short prompts, non-streaming: about 0.91 req/s and about 107 output tokens/s, p50 about 1.15 s. This is the
single-request latency of the engine plus the gateway (see the repeat table above).

### B: increasing concurrency

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 54 | 0 | 0.90 | 105.2 | 1185 | 1414 | 1913 | 0.88 |
| 5 | 170 | 0 | 2.65 | 311.4 | 1721 | 2967 | 3450 | 2.77 |
| 10 | 205 | 0 | 3.23 | 379.9 | 2997 | 4948 | 6718 | 3.29 |
| 25 | 189 | 0 | 2.77 | 324.9 | 7113 | 23651 | 40634 | 2.55 |
| 50 | 218 | 0 | 2.88 | 337.9 | 13404 | 38389 | 55781 | 2.77 |

Throughput rises from 0.90 req/s at 1 client to a peak of 3.23 req/s at 10 clients, then stays about 11 to 14% lower at 25 and 50
clients while latency keeps growing: p50 reaches 13.4 s and p99 55.8 s at 50 clients. That is saturation: the engine is the
bottleneck, the gateway admits 6 at a time, and the rest wait in its queue. Both runs show the same shape (plateau near 3 req/s,
then 11 to 22% lower at 25 and above, depending on the run and the throughput definition), but the drop is of the same size as
run-to-run noise, so its cause is **unexplained** here. This shows where *this* engine saturates; it is not a capacity figure
for anything else.

### C and D: long vs short prompts (streaming)

Long prompts (C):

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 39 | 0 | 0.62 | 80.0 | 1298 | 3166 | 3238 | 0.64 | 334 | 902 | 1341 |
| 4 | 88 | 0 | 1.42 | 181.3 | 2508 | 4072 | 4738 | 1.41 | 842 | 1898 | 1987 |
| 8 | 86 | 0 | 1.25 | 160.0 | 5806 | 11857 | 22503 | 1.37 | 1547 | 7513 | 18668 |

Short prompts (D), same levels and settings:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 34 | 0 | 0.58 | 68.9 | 1422 | 6048 | 6404 | 0.56 | 53 | 226 | 824 |
| 4 | 119 | 0 | 1.85 | 217.6 | 1786 | 5501 | 7541 | 1.88 | 274 | 566 | 777 |
| 8 | 140 | 0 | 2.21 | 260.5 | 3084 | 6825 | 9545 | 2.24 | 821 | 3712 | 7558 |

The robust effect is on **time to first token**: at one client TTFT p50 was 334 ms with long prompts vs 53 ms with short ones
(about 6x), and it reproduced in run 1 (329 vs 54 ms). That is the extra prefill work for longer prompts. Throughput is lower
with long prompts at higher concurrency (1.42 vs 1.85 req/s at 4 clients, 1.25 vs 2.21 at 8; run 1 showed the same direction, 12%
and 32% lower), while at one client the two are similar (D's number is depressed by the stall described above). At 4 and 8 clients
TTFT is dominated by queueing in the gateway, not prefill.

### E: streaming under rising concurrency

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 51 | 0 | 0.85 | 100.2 | 1224 | 1670 | 1750 | 0.85 | 51 | 96 | 444 |
| 5 | 149 | 0 | 2.52 | 295.7 | 2062 | 3488 | 4163 | 2.41 | 236 | 523 | 744 |
| 10 | 178 | 0 | 2.88 | 339.4 | 3165 | 6243 | 10041 | 2.87 | 1290 | 4189 | 7579 |
| 25 | 181 | 0 | 2.54 | 301.3 | 6976 | 22910 | 37778 | 2.61 | 4647 | 20515 | 35497 |

TTFT grows with concurrency (p50 51 ms at 1 client, 4.6 s at 25) because it includes the gateway queue wait once more than 6
requests are in flight. Zero failures at every level, because the gateway's limit keeps the engine inside its safe range.

## Overload protection: the same overload, direct vs through the gateway

16 streaming clients (`max_tokens` 64), straight at the engine and through the gateway.

Direct to the engine:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 16 | 527 | 66 | 7.40 | 454.9 | 2103 | 3129 | 3528 | 7.48 | 355 | 592 | 715 |

Through the gateway:

| conc | reqs | failed | req/s | tok/s | p50 ms | p95 ms | p99 ms | req/s (wall) | TTFT p50 | TTFT p95 | TTFT p99 |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 16 | 281 | 0 | 4.65 | 283.8 | 2928 | 8042 | 12271 | 4.30 | 1887 | 7120 | 10469 |

Direct to the engine, **66 of 527 requests (12.5%) failed** with connection resets (run 1: 64 of 321, 20%). Through the gateway,
**0 of 281 failed**: the limit and queue turned engine-side failures into waiting. It is not free: successful throughput through
the gateway was 4.65 req/s against 7.40 direct (63%; tokens/s 284 vs 455), and tail latency was much higher
(p99 12.3 s vs 3.5 s) because requests queue. The direct latency figures cover only the requests that **succeeded**, so they are biased toward the
lucky ones. The limit of 6 is the last value with no failures in the probe, and this run suggests it is conservative for goodput on this
engine; real engines need their own limit.

## Gateway overhead

How much latency does the gateway add over calling the engine directly? Each paired run alternates direct and gateway runs (swapping
which goes first on every other pair) so slow drift cancels out. Run 2's five pairs:

**concurrency 1**: per-pair results (direct to engine vs via gateway)

| pair | direct p50 ms | gateway p50 ms | p50 diff ms | direct req/s | gateway req/s | req/s diff | failed |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 1100 | 1186 | +85 | 0.88 | 0.88 | +0.0% | 0 |
| 2 | 1161 | 1122 | -39 | 0.94 | 0.81 | -13.3% | 0 |
| 3 | 1209 | 1126 | -82 | 0.81 | 0.88 | +7.7% | 0 |
| 4 | 1158 | 1124 | -34 | 0.94 | 0.94 | +0.0% | 0 |
| 5 | 1114 | 1180 | +65 | 0.88 | 0.94 | +7.1% | 0 |

p50 difference (gateway - direct): median -34 ms, min -82, max +85 over 5 pairs; req/s difference: median +0.0%, min -13.3%, max +7.7%

**concurrency 4**: per-pair results (direct to engine vs via gateway)

| pair | direct p50 ms | gateway p50 ms | p50 diff ms | direct req/s | gateway req/s | req/s diff | failed |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 2111 | 1400 | -711 | 1.81 | 2.69 | +48.3% | 0 |
| 2 | 1452 | 1492 | +40 | 2.38 | 2.62 | +10.5% | 0 |
| 3 | 1580 | 2162 | +582 | 2.06 | 1.69 | -18.2% | 0 |
| 4 | 1454 | 1397 | -56 | 2.50 | 2.69 | +7.5% | 0 |
| 5 | 1592 | 1901 | +309 | 2.44 | 1.94 | -20.5% | 0 |

p50 difference (gateway - direct): median +40 ms, min -711, max +582 over 5 pairs; req/s difference: median +7.5%, min -20.5%, max +48.3%

Pooling the five pairs from each run (10 pairs, all on AC power) gives:

| conc | pairs | mean diff ms | median diff ms | stdev ms | rough 95% interval (mean) ms | min / max ms |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | 10 | -3 | -32 | 68 | -52 to +45 | -82 / +113 |
| 4 | 10 | -31 | -36 | 414 | -322 to +261 | -711 / +582 |

**At one client the gateway's overhead is within roughly -50 to +45 ms (a rough 95% interval around a mean of about -3 ms), about 4% of a
1.2 s request.** That is a bound, not a measurement of the overhead itself: the true cost of parsing a request and relaying a stream is
tiny compared with the engine's own variability, and this setup cannot see it. At four clients the pair-to-pair noise (hundreds of
milliseconds) is far larger than any plausible overhead, so nothing can be concluded there. The interval uses a small sample and assumes
roughly independent pairs. Measuring a proxy's cost properly needs a fast, low-variance backend (for example a no-op engine) and a load
generator on a different machine; that was not done.

## Experiment F: vLLM vs Triton + TensorRT-LLM: **not run**

This comparison needs an NVIDIA GPU machine, and this project was developed on an Apple Silicon laptop, so there are **no
vLLM, Triton or TensorRT-LLM measurements in this repository and none are claimed.** The pieces exist and are tested
without a GPU (the Triton backend against a fake server, the repository checker, the report-comparison tool that refuses
unlike-for-like runs), but the engines themselves have never been run here.

| Engine | Hardware | Model | Result |
|---|---|---|---|
| vLLM | not run | not run | not run |
| Triton + TensorRT-LLM | not run | not run | not run |

To run it, follow the procedure in `gpu-deployment.md`: same GPU machine, same model, one engine behind the gateway at a time,
`inferbench --scenario benchmarks/scenarios/f-backend-comparison.json` with `--engine`, `--hardware` and `--model` set, then
`inferbench compare`. Record the engine versions and the precision, batching and engine-build settings alongside the results.

## Problems found while benchmarking, and what happened to them

Fixed after run 1 (and verified by mutation-checked tests):

- **A single backend could not retry a transient failure.** A connection reset from the only backend was never retried, because
  retries went only to a *different* backend (run 1: 3 of 2,050 gateway requests, 0.15%). The gateway now retries a transport-level
  failure once on the same backend after a short pause; HTTP 5xx/429 from the only backend are deliberately not retried that way.
  In run 2 there were **0 failures in 2347 suite requests and 344 paired requests**, but the gateway ran at error log level, so
  whether any same-backend retry actually fired was **not recorded**, and this does not by itself prove the fix. The runner now logs
  at warn level so retries show up next time.
- **`inferbench` did not request token usage in streams**, so direct streaming runs reported 0 tokens/s. It now does by default
  (`--stream-usage`); the direct overload run above reports 455 tokens/s.
- **Throughput included drain time.** It is now steady-state. As shown above, the old definition was less biased than suspected (about
  +/-9%), so this was a clarity improvement, not a correction of a large error.

Still open:

- **Intermittent single-request stalls** of several seconds, cause unknown, which make p95/p99 and small levels unreliable.
- The **possible heat-while-charging effect** that depressed run 1; not confirmed.
- The **11 to 22% throughput drop at 25+ clients** in experiment B, unexplained.
- **Gateway overhead** is bounded only at one client; it needs a low-variance backend and a separate client machine.
- Everything here is one laptop and a stand-in engine; **no GPU result exists**.

## Reproducing

```bash
make local-model                      # or any OpenAI-compatible engine; see local-development.md
benchmarks/run_local.sh               # main suite, about 25 minutes; refuses to run on battery power
SUITE=overhead DURATION=20s WARMUP=3s benchmarks/run_local.sh    # paired overhead runs, about 8 minutes
python3 benchmarks/tools/results_to_md.py table benchmarks/results/<run>/b-increasing-concurrency.json
python3 benchmarks/tools/results_to_md.py pooled <paired-dir> [<paired-dir> ...]
```

Plug in the charger first and quit heavy applications. `power.log` in the output directory records the power source around every run.
