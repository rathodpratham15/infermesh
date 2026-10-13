# InferMesh

A Go inference gateway that puts one OpenAI-compatible API in front of multiple LLM serving engines (vLLM,
NVIDIA Triton + TensorRT-LLM), with routing, admission control, OpenTelemetry/Prometheus observability, Kafka
telemetry, Kubernetes/Helm deployment and a reproducible benchmark tool.

It does **not** run models itself. The point is the infrastructure and performance-engineering layer around the
engines.

```
 client ──► Go gateway ──► vLLM  /  Triton + TensorRT-LLM  (any OpenAI-compatible engine)
              │  routing · limits · retries · streaming
              ├──► Prometheus / Grafana   (metrics, traces)
              └──► Kafka ──► telemetry consumer
```

## What it does

- **Gateway** (`cmd/gateway`): `POST /v1/chat/completions` (JSON, SSE streaming) and a WebSocket endpoint, health and
  readiness, graceful shutdown. Routing policies `round_robin`, `least_outstanding`, `weighted`; per-backend concurrency
  limits with a bounded queue and `429` load shedding; background health checks; retries on a different backend; request
  timeouts.
- **Backends** behind one `InferenceBackend` interface: `VLLM`, `Triton` (via Triton's OpenAI-compatible frontend) and a
  `Mock`. Any OpenAI-compatible server works, which is how development runs without a GPU.
- **Observability**: OpenTelemetry metrics at `/metrics` (throughput, latency, TTFT, tokens, queue depth, per-backend
  state, errors), traces over OTLP, and a Grafana dashboard.
- **Telemetry events**: `InferenceStarted/Completed/Failed` to Kafka via a non-blocking producer (never in the request path),
  aggregated by a separate consumer.
- **Benchmarking** (`cmd/inferbench`): closed- and open-loop load, exact percentiles, TTFT, tokens/s, JSON reports that record
  the hardware and model, and a `compare` command that refuses unlike-for-like runs.
- **Deployment**: multi-stage Dockerfiles, a Docker Compose stack, and a Helm chart (gateway, optional vLLM and Triton, Kafka,
  consumer, HPA, ServiceMonitor, dashboard).

## Web UI

`frontend/` is a test console (React + Vite + TypeScript + Tailwind): chat with streaming over SSE, WebSocket or plain HTTP, a
multi-line system prompt, backend pinning, live backend fleet status, per-request latency / TTFT / tokens-per-second, and a
session Request Log with JSON export. It has a built-in **mock mode** (clearly labelled) so it runs with no gateway.

```bash
make frontend-install && make up && make frontend-dev     # http://localhost:5173 against the Compose gateway
```

Details and the browser end-to-end test are in [frontend/README.md](frontend/README.md).

## Quick start (no GPU needed)

```bash
make up                                  # gateway (mock backend) + Kafka + consumer + Prometheus + Grafana
curl localhost:8080/v1/chat/completions -d '{"messages":[{"role":"user","content":"hi"}]}'
open http://localhost:3000               # Grafana (admin / admin), dashboard "InferMesh Overview"
make down
```

Other modes (a real local model on Apple Silicon, a CPU container, or NVIDIA GPU engines) are in
[docs/local-development.md](docs/local-development.md). Run the tests with `make test` and `make lint`.

## Status: what is verified and what is not

This was built on an Apple Silicon laptop with **no NVIDIA GPU**. Be clear-eyed about what that means:

| Area | Status |
|---|---|
| Gateway, routing, limits, retries, streaming, metrics, events, `inferbench` | Built and tested (unit tests with the race detector, plus live runs against a real local model and real Kafka) |
| Docker Compose stack | Run end to end locally (mock, host model, CPU llama.cpp container) |
| Web UI (`frontend/`) | Type-checked, built, 21 unit tests, and a **real-browser end-to-end test** (Chromium) against the real gateway checking what goes over the wire. Mock mode is simulated data and says so |
| Helm chart on Kubernetes | Deployed to a local `kind` cluster; CPU autoscaling of the gateway observed (1 to 4 pods and back). **No GPU scheduling** |
| vLLM on an NVIDIA GPU | Configuration only (Compose profile, Helm values). **Never run** |
| Triton + TensorRT-LLM | Backend tested against a **fake** Triton server; image, entrypoint and Helm component validated for syntax and schema only. **Never run** |
| Performance | Local benchmarks measured on an M3 laptop against a small stand-in engine (mlx-lm). **No vLLM or Triton numbers exist** |

The vLLM vs Triton + TensorRT-LLM comparison, GPU autoscaling and anything scheduled on a GPU are explicitly left for a
GPU machine; the runbook is in [docs/gpu-deployment.md](docs/gpu-deployment.md). Nothing in this repository fabricates
benchmark, GPU or autoscaling results.

Known gaps: no authentication or per-client rate limiting; latency-aware routing is not implemented;
intermittent multi-second request stalls were seen on the benchmark laptop and are unexplained (see
[docs/performance.md](docs/performance.md)).

## Documentation

| | |
|---|---|
| [Architecture](docs/architecture.md) | Components, request path, backpressure, deployment shapes |
| [Local development](docs/local-development.md) | Running locally and under Docker Compose; configuration reference |
| [Observability](docs/observability.md) | Metrics, traces, dashboard |
| [Telemetry events](docs/telemetry-events.md) | Kafka schema, delivery guarantees, consumer |
| [Benchmarking](docs/benchmarking.md) | `inferbench`, scenarios, comparing backends |
| [Performance](docs/performance.md) | Measured results and the problems found, with all caveats |
| [Kubernetes](docs/kubernetes.md) | Helm chart, autoscaling signals, what was verified |
| [GPU deployment](docs/gpu-deployment.md) | vLLM and Triton + TensorRT-LLM: what needs a GPU, how to run and compare them |

## Repository layout

```
cmd/            gateway, telemetry-consumer, inferbench, tritonrepo-check
frontend/       web UI (React + Vite), unit tests, optional browser e2e
internal/       gateway, router, backend, config, metrics, telemetry, events, consumer, bench, tritonrepo, health
deploy/         docker/ (Dockerfile), helm/infermesh/, triton/ (image for Triton + TensorRT-LLM), grafana/, prometheus/
benchmarks/     datasets/, scenarios/, run_local.sh, tools/, recorded/ (raw results)
docs/
docker-compose.yml  Makefile
```

## License

MIT
