# Telemetry events (Kafka)

Kafka carries **asynchronous telemetry only**. It is never in the synchronous inference
path: the gateway hands each event to a non-blocking producer and moves on.

```
gateway --(non-blocking)--> Kafka topic --> telemetry-consumer --> Prometheus metrics
```

## Events

Three event types are published as JSON to `KAFKA_TOPIC` (default `infermesh.inference.events`),
keyed by `request_id`:

| Type | When |
|---|---|
| `InferenceStarted` | A backend slot was acquired for an attempt (a retried request emits one per attempt, `attempt` = 1, 2, ...) |
| `InferenceCompleted` | The generation finished successfully |
| `InferenceFailed` | The request ended in failure after retries (`status`: `error`, `timeout`, `rejected` (shed or no healthy backend), `canceled` (client left)) |

Example (`InferenceCompleted`, streaming):

```json
{"event_type":"InferenceCompleted","request_id":"req-60fd61d36fac116f","timestamp":"2026-10-08T22:44:12.881776Z",
 "backend":"vllm","model":"x","stream":true,"attempt":1,"queue_ms":0,"ttft_ms":39,
 "input_tokens":33,"output_tokens":24,"generation_ms":287,"status":"success"}
```

Field notes: `queue_ms` is time waiting for a backend slot; `ttft_ms` is present only for
streaming requests; `generation_ms` is the time after the first token when streaming and the
whole backend call otherwise. Token counts are what the backend reports. Requests rejected as
invalid (HTTP 400) never reach a backend and produce no events.

## Delivery guarantees

- **Gateway -> Kafka is best effort.** `Publish` uses a non-blocking producer with a bounded
  buffer (10,000 records). If the broker is slow or down, buffered events wait and, once the
  buffer is full, new events are **dropped** and counted in
  `infermesh_events_published_total{result="dropped"|"error"}`. Requests are unaffected. On
  shutdown the gateway flushes for up to 5s.
- **Kafka -> consumer is at-least-once.** Offsets are committed only after an event has been
  aggregated, so after a crash some events can be counted twice, none are lost. A new
  consumer group starts from the earliest offset.
- A malformed record is skipped and counted (`infermesh_telemetry_events_invalid_total`); it
  does not stall the partition.

Because delivery is lossy under outages, treat these metrics as telemetry, not billing data.
The gateway's own `/metrics` (see `observability.md`) remains the source of truth for request
counts and latency.

## Consumer

`cmd/telemetry-consumer` joins a consumer group and serves aggregated metrics at
`:9100/metrics`:

| Series | Meaning |
|---|---|
| `infermesh_telemetry_events_total{event_type,backend,status}` | Events consumed |
| `infermesh_telemetry_tokens_input_total` / `..._output_total{backend}` | Tokens in completed inferences |
| `infermesh_telemetry_queue_duration_seconds{backend}` | Queue wait (histogram) |
| `infermesh_telemetry_ttft_seconds{backend}` | Time to first token, streaming (histogram) |
| `infermesh_telemetry_generation_duration_seconds{backend}` | Generation time (histogram) |
| `infermesh_telemetry_retried_requests_total{backend}` | Completed requests that needed more than one attempt |
| `infermesh_telemetry_events_invalid_total{reason}` | Skipped records |

## Configuration

| Variable | Used by | Meaning | Default |
|---|---|---|---|
| `KAFKA_BROKERS` | gateway, consumer | comma-separated brokers; **empty disables events in the gateway**, required by the consumer | none |
| `KAFKA_TOPIC` | both | topic | `infermesh.inference.events` |
| `KAFKA_GROUP` | consumer | consumer group | `infermesh-telemetry` |
| `METRICS_ADDR` | consumer | metrics listen address | `:9100` |

## Running it locally

```bash
docker run -d --name kafka -p 9092:9092 apache/kafka:3.9.0        # single-node KRaft broker
KAFKA_BROKERS=localhost:9092 go run ./cmd/gateway                  # publishes events
KAFKA_BROKERS=localhost:9092 go run ./cmd/telemetry-consumer       # aggregates them
curl localhost:9100/metrics | grep infermesh_telemetry
```

Docker Compose wiring comes in the Compose phase. The live round-trip test runs against a
broker with `INFERMESH_TEST_KAFKA_BROKERS=localhost:9092 go test ./internal/consumer -run KafkaRoundTrip`.
