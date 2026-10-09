# Architecture

InferMesh is a Go gateway that sits in front of one or more LLM serving engines and gives clients a single
OpenAI-compatible API, with routing, admission control, observability and asynchronous telemetry. It does
not run models itself.

```
 Client / curl / inferbench
          |  HTTP, SSE, WebSocket
          v
 +-----------------------------------------------+        +-------------------+
 |                 Go gateway                    |  Kafka |  telemetry        |
 |  parse -> route -> admit -> call -> stream    +------->|  consumer         |
 |  retries, timeouts, health, queue, metrics    | events |  (aggregates)     |
 +----------+----------------------+-------------+        +---------+---------+
            |                      |                                |
            v                      v                                v
   +-----------------+    +------------------------+         Prometheus <- /metrics (gateway, consumer)
   |  vLLM backend   |    |  Triton backend        |                |
   |  (OpenAI API)   |    |  (OpenAI frontend)     |                v
   +-----------------+    +------------------------+             Grafana
     any OpenAI-compatible engine: vLLM on an NVIDIA GPU, or mlx-lm / llama.cpp locally
```

The same architecture runs on a laptop (mock backend, or a local stand-in engine), under Docker Compose, and on
Kubernetes. Only the engine behind the gateway changes. NVIDIA-specific components are optional profiles
(`gpu-deployment.md`).

## Request path

1. **Parse and validate** (`internal/gateway`): OpenAI-shaped JSON; a request ID is taken from `X-Request-ID` or
   generated; W3C trace context is extracted.
2. **Admit and route** (`internal/router`): a `Pool` picks a backend with the configured policy
   (`round_robin`, `least_outstanding`, `weighted`) among healthy backends with a free slot. If every healthy backend is at its
   `MAX_CONCURRENT`, the request waits in a bounded queue (`MAX_QUEUE`, within `REQUEST_TIMEOUT`); beyond that the
   gateway sheds load with `429` and `Retry-After`. With no healthy backend it returns `503`.
3. **Call the engine** through the `InferenceBackend` interface (`Generate`, `Stream`, `Health`). `VLLM` and `Triton`
   share one OpenAI client and differ only in the health endpoint and the stream-usage option.
4. **Stream or return.** For SSE and WebSocket the HTTP status is committed only after the first backend token, so
   early failures are ordinary JSON errors; later failures are reported in-band. Client disconnect cancels the engine call.
5. **Retry**: a retryable failure (5xx, 429, connection error) before any output is retried on a *different* backend, up
   to `MAX_RETRIES`. If no other backend is available and the failure was a transport-level one (for example a
   connection reset), the same backend is retried once after a short pause (`SameBackendRetryDelay`); HTTP 5xx/429
   from the only backend are not, since repeating them adds load to a server that is already failing. Timeouts,
   cancellations and 4xx are never retried.
6. **Record**: metrics, a span per attempt, and a Kafka event (off the critical path).

Backend health is polled in the background and cached; routing never makes a network call to choose a backend.

## Endpoints

`POST /v1/chat/completions` (JSON or SSE), `GET /v1/chat/stream` (WebSocket), `GET /v1/models`, `GET /v1/backends`,
`GET /healthz`, `GET /readyz`, `GET /metrics`. Browser clients use CORS and optional per-request backend pinning
(`local-development.md`, "Browser clients").

## Backpressure and failure containment

The per-backend limit is the main protection for engines. A real engine batches many requests, but some engines
(`mlx_lm.server`, used locally) reset connections when too many arrive at once. The gateway limits in-flight
requests per backend and queues the rest, so overload shows up as queueing latency and `429`s rather than
engine-side connection failures (measured in `performance.md`).

## Observability and telemetry (two separate paths)

- **Metrics and traces** (synchronous instrumentation, `observability.md`): OpenTelemetry instruments exposed at
  `/metrics` in Prometheus format; spans exported over OTLP when configured. TTFT, tokens/s, queue depth, per-backend
  active requests and health, routing decisions, errors by class.
- **Events** (asynchronous, `telemetry-events.md`): `InferenceStarted/Completed/Failed` published to Kafka with a
  non-blocking producer and a bounded buffer. A Kafka outage can lose events but cannot slow or fail a request. A
  separate consumer aggregates them into its own Prometheus metrics. Kafka is never in the inference path.

## Deployment shapes

| Shape | Where | Engine |
|---|---|---|
| Single binary | `go run ./cmd/gateway` | mock, or any OpenAI-compatible server |
| Docker Compose | `docker-compose.yml` | mock, host model, llama.cpp (CPU), vLLM or Triton (NVIDIA GPU) |
| Kubernetes | `deploy/helm/infermesh` | mock by default; vLLM or Triton on GPU nodes via values files |

Container images are static Go binaries on a distroless non-root base (`deploy/docker/Dockerfile`).

## Package map

| Path | Responsibility |
|---|---|
| `cmd/gateway` | The gateway binary |
| `cmd/telemetry-consumer` | Kafka consumer that aggregates events into metrics |
| `cmd/inferbench` | Load generator and report/compare tool |
| `frontend/` | Web test console (React + Vite): streaming chat, backend pinning, fleet status, request log. Calls the gateway over HTTP/SSE/WebSocket |
| `cmd/tritonrepo-check` | Sanity checker for Triton model repositories |
| `internal/gateway` | HTTP, SSE and WebSocket handlers, retries, per-request instrumentation |
| `internal/router` | Routing policies, concurrency limits, queue, health checks |
| `internal/backend` | `InferenceBackend`, the shared OpenAI client, `VLLM`, `Triton`, `Mock` |
| `internal/config` | Environment-variable configuration |
| `internal/metrics`, `internal/telemetry` | OTel metrics (Prometheus) and tracing setup |
| `internal/events`, `internal/consumer` | Kafka events, producer and aggregator |
| `internal/bench`, `internal/tritonrepo`, `internal/health` | Benchmark engine, repository checker, container health probe |

## Deliberate limits

- One gateway tier with in-memory routing state; replicas do not share queue or health state. Routing across replicas
  relies on the platform's load balancing.
- No authentication, rate limiting per client, or request-content inspection.
- Latency-aware routing is not implemented (the latency EWMA is tracked but unused for routing).
- Kafka in the Helm chart is single-node and meant for telemetry demos.
