# Observability

The gateway is instrumented with OpenTelemetry. Metrics are created through the OTel
metrics API and exposed in Prometheus format at `GET /metrics`; traces are exported over
OTLP/HTTP when an endpoint is configured.

## Metrics

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `infermesh_requests_total` | counter | `route`, `code` | Requests handled (`route` is `chat` for HTTP/SSE, `ws` for WebSocket) |
| `infermesh_request_errors_total` | counter | `route`, `code` | Status >= 400, plus `code="stream_aborted"` for streams that failed after the response started |
| `infermesh_request_duration_seconds` | histogram | `route`, `code` | End-to-end latency including queueing and the full stream |
| `infermesh_requests_active` | gauge | `route` | Requests in flight in the gateway |
| `infermesh_queue_depth` | gauge | | Requests waiting for a free backend slot |
| `infermesh_backend_active` | gauge | `backend` | Slots in use per backend |
| `infermesh_backend_healthy` | gauge | `backend` | 1 if the last health probe succeeded |
| `infermesh_backend_duration_seconds` | histogram | `backend` | Time a request held a backend slot (successful attempts) |
| `infermesh_backend_failures_total` | counter | `backend`, `reason` | Failed attempts: `http_5xx`, `http_4xx`, `rate_limited`, `timeout`, `connection`, `other` |
| `infermesh_routing_decisions_total` | counter | `backend`, `policy` | Backend picks (a retry is a second decision) |
| `infermesh_ttft_seconds` | histogram | `backend` | Time to first token, **streaming requests only** |
| `infermesh_tokens_generated_total` / `infermesh_tokens_prompt_total` | counter | `backend` | Token counts as reported by the backend |
| `infermesh_generation_rate_per_second` | histogram | `backend` | Per-request decode rate: completion tokens / generation time (after TTFT when streaming) |

Notes on what these do and don't tell you:

- TTFT is measured **at the gateway**, from the moment a backend attempt starts to its first token. It is not available for non-streaming requests.
- Token counts come from backend-reported usage. If a backend does not report usage, those series are not updated.
- Aggregate tokens/s is `rate(infermesh_tokens_generated_total[...])`; the histogram is the per-request rate.
- Go runtime and process metrics are exported alongside (`go_*`, `process_*`).
- **There are no GPU metrics.** GPU utilization comes from NVIDIA DCGM on a GPU node and is not produced by this gateway.
- `/metrics` is served on the main listener. Do not expose it publicly; restrict it at the network layer.

## Traces

Tracing is off (a no-op) unless one of the standard variables is set:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318   # OTLP/HTTP
OTEL_SERVICE_NAME=infermesh-gateway                 # optional override
```

Spans: `chat.completions` (or `chat.stream.ws`) per request, with a child `backend.attempt`
per attempt (so a retry shows as two). W3C `traceparent` headers on incoming requests are
honoured, and trace context is forwarded to the backend. Failed attempts record the error.

## Dashboard

`deploy/grafana/dashboards/infermesh.json` has panels for throughput, p50/p95/p99 latency,
TTFT, tokens/s, active requests and queue depth, backend distribution, error rate, backend
health and Kubernetes pod count. The pod-count panel needs kube-state-metrics and is empty
outside Kubernetes. Provisioning files and a Prometheus scrape config are under
`deploy/grafana/provisioning/` and `deploy/prometheus/`; Docker Compose wiring comes with the
Compose phase.
