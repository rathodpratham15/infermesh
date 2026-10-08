# Local development (Apple Silicon, no NVIDIA GPU)

Everything on this page runs on a Mac. NVIDIA-only components (Triton,
TensorRT-LLM) are covered separately in `gpu-deployment.md` once they exist.

## Run the gateway with the mock backend

```bash
make run                      # BACKENDS defaults to "mock"
curl -s localhost:8080/v1/chat/completions \
  -d '{"model":"m","messages":[{"role":"user","content":"hi"}]}'
```

The mock returns a fixed reply with a configurable delay. It says nothing about
real inference performance.

## Run the gateway against a real local model

```bash
make local-model              # terminal 1: serves a 0.5B 4-bit model on :8000
make run-vllm                 # terminal 2: gateway -> local model
```

### What "vLLM backend" means locally

The gateway's `vllm` backend speaks the OpenAI-compatible HTTP API that vLLM
exposes (`/v1/chat/completions`, SSE streaming, `/v1/models`). On a Mac there is
no CUDA, so `make local-model` serves the model with `mlx_lm.server` (Apple's
MLX), which implements the same API. This was checked directly against
`mlx_lm.server` 0.32; **real vLLM has not been run yet**. It is validated on a
GPU machine in the GPU deployment phase. A `vllm-metal` package was not found on
PyPI, so it is not used.

Because the contract is the same, switching to real vLLM is configuration only:

```bash
BACKENDS=vllm
BACKEND_VLLM_URL=http://<vllm-host>:8000
BACKEND_VLLM_MODEL=<any model vLLM is serving>   # e.g. a larger model on a GPU box
```

The default local model (`Qwen2.5-0.5B-Instruct-4bit`) was chosen to fit
comfortably in 16 GB. Do not assume an 8B model fits on a laptop; override
`LOCAL_MODEL` to try others.

## Docker Compose

The whole stack runs from `docker-compose.yml` on any machine with Docker, with no NVIDIA GPU:
gateway, Kafka (single-node KRaft), telemetry consumer, Prometheus and Grafana. Images are built from
one multi-stage `deploy/docker/Dockerfile` (static binary on a distroless, non-root base; the build
cross-compiles, so no emulation on Apple Silicon).

| Command | Engine behind the gateway |
|---|---|
| `make up` | `mock` backend: no model, no GPU. Good for trying the stack and for load-testing the gateway itself |
| `make up-host-model` | A real model served **on the host** (`make local-model`, mlx-lm on Apple Silicon); the gateway reaches it at `host.docker.internal:8000` |
| `make up-cpu-model` | A **llama.cpp** container serving a small GGUF model on CPU. Fully self-contained; the first start downloads the model |
| `make up-gpu` | **vLLM in a container on an NVIDIA GPU.** Requires an NVIDIA GPU and the NVIDIA Container Toolkit. See below |

`make down` stops everything (volumes are kept). The architecture is identical in every mode; only the
engine changes, selected with `BACKENDS` / `BACKEND_VLLM_URL` / `BACKEND_VLLM_MODEL`.

| Service | URL (bound to 127.0.0.1 only) |
|---|---|
| Gateway | http://localhost:8080 (`/v1/chat/completions`, `/healthz`, `/readyz`, `/v1/backends`, `/metrics`) |
| Grafana | http://localhost:3000, login `admin` / `admin` (override with `GRAFANA_USER` / `GRAFANA_PASSWORD`); dashboard *InferMesh Overview* |
| Prometheus | http://localhost:9090 |
| Consumer metrics | http://localhost:9100/metrics |
| Kafka (from the host) | `localhost:29092`; containers use `kafka:9092` |

Notes:

- The llama.cpp engine is a CPU **stand-in** for vLLM, like mlx-lm: the gateway only needs an
  OpenAI-compatible server. It is not a performance proxy for vLLM on a GPU.
- `host.docker.internal` reaches a loopback-bound host service on Docker Desktop (macOS/Windows). On
  Linux, bind the host model to `0.0.0.0` (or the Docker bridge address); the compose file maps the name
  with `host-gateway`.
- Containers have health checks (`/app healthcheck`; distroless images have no shell or curl), and
  `docker compose stop` shuts the gateway down gracefully within a second.
- Change the default Grafana password if the ports are ever exposed beyond localhost.

### GPU profile (not tested on the development machine)

`docker compose --profile gpu` adds a `vllm` service (`vllm/vllm-openai`, pinned by `VLLM_VERSION`) with
an `nvidia` device reservation (`VLLM_GPUS`, default 1). Set `VLLM_MODEL` (default
`Qwen/Qwen2.5-0.5B-Instruct`; use a larger model on a real GPU) and, for gated models, `HF_TOKEN`. The
file was validated with `docker compose config` only; it has **not been run**, because this repo was
developed on an Apple Silicon Mac. Triton + TensorRT-LLM is added in a later phase under `deploy/triton/`.

## Streaming

```bash
# Server-sent events (OpenAI-compatible chunks, ends with `data: [DONE]`)
curl -N localhost:8080/v1/chat/completions \
  -d '{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

WebSocket: connect to `ws://localhost:8080/v1/chat/stream`, send one JSON chat
request, and read frames `start` (request ID, backend), `token` (text),
`done` (finish reason, usage, `ttft_ms`) or `error`, after which the server
closes. Browsers on another origin (e.g. the Vite dev server) must be listed in
`WS_ALLOWED_ORIGINS`, e.g. `WS_ALLOWED_ORIGINS=localhost:5173`.

Errors that happen before the first token are normal JSON errors (502/504). After
the first token the HTTP status is already sent, so SSE reports a failure as a
final `data: {"error": ...}` event. Closing the connection cancels the backend call.

## Routing and concurrency

Policies (`ROUTING_POLICY`):

- `round_robin`: rotate through healthy backends that have a free slot.
- `least_outstanding`: send to the backend with the fewest in-flight requests; ties rotate.
- `weighted`: smooth weighted round robin using `BACKEND_<NAME>_WEIGHT` (a 3:1 weight gives an exact 3:1 split).

Each backend accepts at most `BACKEND_<NAME>_MAX_CONCURRENT` requests at once. When every
healthy backend is full, up to `MAX_QUEUE` requests wait for a slot (bounded by `REQUEST_TIMEOUT`);
beyond that the gateway sheds load with `429` and `Retry-After`. With no healthy backend it
returns `503`.

Health is probed every `HEALTH_INTERVAL` and cached, so routing never makes a network call just
to pick a backend. A failed attempt that is retryable (5xx, 429, connection errors) is retried on
a *different* backend up to `MAX_RETRIES` times, but only before any output has reached the
client. Timeouts, cancellations and 4xx responses are not retried.

`GET /v1/backends` shows per-backend active/completed/failure counts, latency EWMA and health,
plus the current queue depth.

## Configuration

| Variable | Meaning | Default |
|---|---|---|
| `BACKENDS` | comma-separated backend names | `mock` |
| `BACKEND_<NAME>_KIND` | `mock`, `vllm` | the name |
| `BACKEND_<NAME>_URL` | engine base URL (required for `vllm`) | - |
| `BACKEND_<NAME>_MODEL` | model sent upstream; overrides the request's | request's |
| `BACKEND_<NAME>_TIMEOUT` | per-request timeout (covers a whole stream) | `60s` |
| `BACKEND_<NAME>_WEIGHT` / `_MAX_CONCURRENT` | weighted routing share / in-flight limit | `1` / `64` |
| `ROUTING_POLICY` | `round_robin`, `least_outstanding`, `weighted` | `round_robin` |
| `MAX_RETRIES` / `MAX_QUEUE` | retries on another backend / requests allowed to wait | `1` / `128` |
| `REQUEST_TIMEOUT` / `HEALTH_INTERVAL` | whole-request budget / health probe period | `2m` / `5s` |
| `KAFKA_BROKERS` / `KAFKA_TOPIC` | telemetry events to Kafka (see `telemetry-events.md`) | disabled / `infermesh.inference.events` |
| `WS_ALLOWED_ORIGINS` | browser origins allowed on the WebSocket | none (same-origin only) |
| `LISTEN_ADDR`, `SHUTDOWN_TIMEOUT`, `LOG_LEVEL` | gateway process | `:8080`, `15s`, `info` |

## Tests

```bash
make test        # unit tests, race detector
make lint
# opt-in test against a live OpenAI-compatible server:
INFERMESH_TEST_VLLM_URL=http://127.0.0.1:8000 \
INFERMESH_TEST_VLLM_MODEL=mlx-community/Qwen2.5-0.5B-Instruct-4bit \
  go test -run TestVLLMLive -v ./internal/backend
```
