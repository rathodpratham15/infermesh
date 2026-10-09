# Kubernetes deployment (Helm)

The chart is `deploy/helm/infermesh/`. It deploys the gateway, an optional vLLM engine, an optional
Triton engine, a single-node Kafka, the telemetry consumer, and the monitoring glue (scrape
annotations, an optional ServiceMonitor, a Grafana dashboard ConfigMap). Nothing in the base chart is
cloud-provider specific, and no GPU is needed unless you enable one.

| Values file | Purpose |
|---|---|
| `values.yaml` | Defaults. Gateway with the built-in **mock** backend, Kafka, consumer. No GPU |
| `values-local.yaml` | Local cluster (kind, minikube, Docker Desktop): one replica, small requests, locally built images |
| `values-gpu.yaml` | NVIDIA GPU cluster: enables vLLM with `nvidia.com/gpu: 1`, a GPU toleration and a persistent model cache. **Not tested on a GPU** |

## Quick start on a local cluster

```bash
make images                                           # docker compose build -> infermesh-gateway:dev, ...
kind create cluster --name infermesh
kind load docker-image infermesh-gateway:dev infermesh-telemetry-consumer:dev --name infermesh
helm install im deploy/helm/infermesh -n im --create-namespace -f deploy/helm/infermesh/values-local.yaml --wait
kubectl -n im port-forward svc/im-infermesh-gateway 8080:8080
curl localhost:8080/readyz
```

With no engine configured the gateway serves the mock backend. To route to a real model set
`gateway.backends` (any OpenAI-compatible URL) or enable `vllm`.

## What the chart creates

- **Gateway**: Deployment (rolling update, `maxUnavailable: 0`, graceful 30s termination), Service, ConfigMap
  (all `BACKEND_*` settings are derived from values; a config change rolls the pods via a checksum
  annotation), optional HPA and PodDisruptionBudget. Readiness uses `/readyz` (fails when no backend is
  healthy), liveness uses `/healthz`. Runs as non-root (uid 65532) with a read-only root filesystem,
  dropped capabilities and a `RuntimeDefault` seccomp profile.
- **vLLM** (`vllm.enabled`): Deployment (`Recreate`, so a rolling update does not need a second GPU),
  Service, generous startup probe for model loading, `/dev/shm` as a memory-backed volume, optional model
  cache PVC and Hugging Face token Secret (or `vllm.existingSecret`). The image, args, model, resources,
  `nodeSelector`, `tolerations`, `affinity` and `runtimeClassName` are all values. GPUs are requested as
  `resources.limits."nvidia.com/gpu"`. The gateway's backend URL is derived automatically.
- **Triton** (`triton.enabled`, `values-triton.yaml`): Deployment, Service and optional PVCs for the model
  repository and engine cache, running the image built from `deploy/triton/Dockerfile` (Triton + TensorRT-LLM
  behind Triton's OpenAI-compatible frontend). It is registered automatically as the gateway's `triton` backend.
  `triton.image.tag` and `triton.model` are required (rendering fails without them). NVIDIA GPU only; see
  `gpu-deployment.md`. Not tested on a GPU.
- **Kafka**: single-node KRaft StatefulSet (telemetry only; events are lost on restart unless
  `kafka.persistence.enabled`). For real use set `kafka.enabled=false` and `kafka.externalBrokers`. With
  neither, events are disabled and the consumer is not deployed.
- **Telemetry consumer**: Deployment and Service (`:9100/metrics`).
- **Monitoring**: `prometheus.io/*` scrape annotations; a `ServiceMonitor` (`monitoring.serviceMonitor.enabled`,
  needs the Prometheus Operator CRDs); and the dashboard as a ConfigMap labelled `grafana_dashboard: "1"`
  for the kube-prometheus-stack Grafana sidecar. The chart does not install Prometheus or Grafana.

## Autoscaling: what each signal means

`gateway.autoscaling.enabled` creates an HPA for the **gateway**. The signals differ a lot in what they
tell you and what they need:

| Signal | What it measures | Needs | Fit here |
|---|---|---|---|
| **CPU utilization** (default) | Gateway CPU as a share of its request | metrics-server | Good for the gateway, whose cost scales with request and stream volume. **Poor for inference pods**, which are GPU-bound: their CPU says almost nothing about load |
| **Request concurrency** (`infermesh_requests_active`, `infermesh_backend_active`) | In-flight requests per pod, compared with the configured `maxConcurrent` | A custom-metrics pipeline (Prometheus + prometheus-adapter, or KEDA) | The optional `gateway.autoscaling.activeRequests` Pods metric. Tracks demand directly, including time spent waiting on a slow backend |
| **Queue depth** (`infermesh_queue_depth`) | Requests waiting because every backend is at its limit | Same pipeline | The clearest *saturation* signal: a non-zero depth means capacity is exhausted. Preferred for scaling the **backends** |
| **GPU utilization** (e.g. DCGM `DCGM_FI_DEV_GPU_UTIL`) | Fraction of time the GPU had work | NVIDIA DCGM exporter + adapter | Lags and misleads: with continuous batching a GPU can show high utilization well below its throughput limit, so it is a weak scale-out trigger. Useful for dashboards |

Scaling **GPU workloads** (`vllm.replicaCount`, or an HPA/KEDA object you add) differs from scaling the
gateway: each new replica needs a free GPU (so possibly a new node), then minutes to pull the image and
load the model. Scale-out is therefore slow, which argues for a non-zero minimum, headroom, and a signal
that moves early (queue depth) rather than late (GPU utilization). The chart does not ship an autoscaler for
the engines.

## What was verified, and what was not

Verified on a local **kind** cluster (Kubernetes v1.37, Apple Silicon, Docker Desktop), with real objects
created by `helm install`:

- The chart renders and passes `helm lint` and `kubeconform` (Kubernetes 1.31 schemas) for the default,
  local, GPU, "everything enabled", external-Kafka and no-Kafka configurations.
- With `values-local.yaml`: the gateway, in-cluster Kafka and consumer start; requests (including
  streaming) work; events flow gateway -> Kafka -> consumer; probes, scrape annotations and the security
  context are as rendered; the dashboard ConfigMap is byte-for-byte the source file.
- **CPU HPA on the gateway**: under load from an in-cluster `inferbench` Job the HPA scaled the gateway from
  1 to 4 replicas (event: *cpu resource utilization above target*) and back to 1 after the stabilization
  window. The backend was the mock, so this shows the autoscaling mechanics, not inference performance.
- The `vllm` component's wiring (Deployment, Service, derived backend URL, probes) was exercised with a
  llama.cpp image as a CPU stand-in engine (see the verification notes in the PR).

**Not verified** (no NVIDIA hardware or extra infrastructure here):

- Anything scheduled on a GPU: `values-gpu.yaml`, `values-triton.yaml`, `nvidia.com/gpu` scheduling, the real
  `vllm/vllm-openai` image and its arguments, `runtimeClassName`, and the Triton component. These were only
  rendered and schema-validated.
- GPU autoscaling of any kind, and the optional `activeRequests` custom-metric HPA (it needs a
  custom-metrics pipeline that was not installed).
- The `ServiceMonitor` against a live Prometheus Operator, and the Grafana sidecar picking up the dashboard.
- The "Kubernetes pod count" dashboard panel (needs kube-state-metrics).
- Multi-node behaviour: a single-node kind cluster cannot show topology or node-failure behaviour.
