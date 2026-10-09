# Recorded benchmark results

Raw `inferbench` JSON, the captured environment and logs for each recorded run. Tables in
`docs/performance.md` are generated from these files by `benchmarks/tools/results_to_md.py`.

| Directory | Run | Notes |
|---|---|---|
| `2026-10-local-m3-run2/` | **Primary.** AC power from start to finish (battery 96-99%), Low Power Mode off, clean tree (commit `f049838`; the Go code is identical to `eb1f7fa` in the PR that adds these files), power source logged around every run in `power.log` | Uses the current `inferbench` (steady-state throughput, token usage in streams) and the same-backend retry |
| `2026-10-local-m3/` | **Superseded; kept for transparency.** Started on battery, plugged in partway through; older `inferbench` (no steady-state figure) | Experiments C, D and E in this run were measured while the machine was charging at about 70% and are much slower than the same experiments in run 2; do not use them as absolute numbers |

Both runs: Apple M3 laptop, `mlx_lm.server` 0.32.0 with `Qwen2.5-0.5B-Instruct-4bit`, client, gateway and engine on one
machine. They say nothing about vLLM, Triton or TensorRT-LLM.
