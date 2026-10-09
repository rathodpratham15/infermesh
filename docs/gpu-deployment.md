# NVIDIA GPU deployment (vLLM and Triton + TensorRT-LLM)

InferMesh develops and runs locally with no NVIDIA GPU. This page covers the parts that **need** one, how
they are wired, and, importantly, what has and has not been tested.

## What needs an NVIDIA GPU

| Component | Needs NVIDIA GPU? | Notes |
|---|---|---|
| Go gateway, router, `inferbench`, Kafka, consumer, Prometheus, Grafana | No | Run anywhere |
| Local engine stand-ins (mlx-lm, llama.cpp) | No | Development only; not performance proxies |
| **vLLM** (`vllm/vllm-openai`) | **Yes** | Compose `--profile gpu`; Helm `values-gpu.yaml` |
| **Triton + TensorRT-LLM** | **Yes** | Compose `--profile triton`; Helm `values-triton.yaml`; image from `deploy/triton/Dockerfile` |
| `tritonrepo-check`, the Triton backend's unit tests | No | Run on a laptop; they test logic, not Triton |

## How the Triton path was chosen

The gateway's `TritonBackend` (`internal/backend/triton.go`) talks to **Triton's OpenAI-compatible frontend**,
the same wire contract as vLLM, so the gateway routes between engines without knowing which is which.
This follows NVIDIA's documentation rather than the older workflow of converting a model to a TensorRT engine
by hand and writing the `config.pbtxt` files yourself:

- NVIDIA documents serving TensorRT-LLM models through the [OpenAI-compatible frontend][openai-fe]
  (`/v1/chat/completions`, `/v1/models`, `/health/ready`, `/metrics`; port 9000 in its examples).
- The [`triton-cli`][triton-cli] (BETA) generates the model repository and **builds the TensorRT-LLM engine
  for you** (`triton import -m <model> --backend tensorrtllm`) and starts the OpenAI frontend
  (`triton start --frontend openai`). `deploy/triton/` wraps exactly that.
- The [TensorRT-LLM backend README][trtllm-backend] still documents the manual route (`trtllm-build`, then
  filling templates). It is supported as "bring your own repository": mount one and the entrypoint will validate
  it and serve it.
- Triton's native `/v2/models/<m>/generate` endpoint is not used: it is marked provisional and its TRT-LLM models
  require engine-specific fields (`bad_words`, `stop_words`) that would leak engine details into the gateway.

**Version pairing is yours to choose.** The `triton-cli` documents its compatible container releases only up to
`25.02` (CLI `0.1.3`), while newer Triton containers exist. The image's build args `TRITON_TAG` and
`TRITON_CLI_REF` are therefore **required with no defaults**. Verify a pair before relying on it.

[openai-fe]: https://docs.nvidia.com/deeplearning/triton-inference-server/user-guide/docs/client_guide/openai_readme.html
[triton-cli]: https://github.com/triton-inference-server/triton_cli
[trtllm-backend]: https://docs.nvidia.com/deeplearning/triton-inference-server/archives/triton-inference-server-2670/user-guide/docs/tensorrtllm_backend/README.html

## Running Triton

Prerequisites: an NVIDIA GPU machine, the NVIDIA driver and Container Toolkit, a Hugging Face token for gated
models, and **a lot of host RAM for the engine build** (the CLI documents 48 GB or more for 7B-8B Llama models).

### Docker Compose

```bash
export TRITON_TAG=25.02            # Triton container release  -> nvcr.io/nvidia/tritonserver:25.02-trtllm-python-py3
export TRITON_CLI_REF=0.1.3        # triton_cli ref compatible with that release (check its table)
export TRITON_IMPORT_MODEL=llama-3.1-8b-instruct          # built on first start if /models is empty
export TRITON_TOKENIZER=meta-llama/Llama-3.1-8B-Instruct  # chat template tokenizer (recommended)
export TRITON_MODEL_NAME=llama-3.1-8b-instruct            # client-facing model name the gateway sends
export HF_TOKEN=...
make up-triton
```

The first start builds the engine (slow). Engines and the model repository live on named volumes, so later
starts skip that. Before loading, the entrypoint runs `tritonrepo-check` on the repository and refuses to start if
it finds errors. To bring your own repository instead, mount it at `/models` and leave `TRITON_IMPORT_MODEL` empty.

### Kubernetes

```bash
helm install im deploy/helm/infermesh -n im --create-namespace \
  -f deploy/helm/infermesh/values-triton.yaml \
  --set triton.image.repository=<registry>/infermesh-triton --set triton.image.tag=<tag> \
  --set triton.model=<client-facing model name> --set triton.importModel=<model to import>
```

Add a `nodeSelector` for your provider's GPU pool in your own values file; nothing cloud-specific is built in.

### Checking a model repository on your laptop

```bash
make triton-check REPO=/path/to/model_repo
```

Catches missing models, unfilled `${...}` template placeholders (with line numbers), a wrong backend, name
mismatches and missing version directories, with no GPU. Names that vary between TensorRT-LLM releases are
reported as warnings, not errors. It does **not** load the model or inspect engines.

## Comparing vLLM and Triton + TensorRT-LLM (experiment F)

A fair comparison needs **the same hardware, the same model and the same load**, with one engine behind the
gateway at a time (with both enabled the gateway splits traffic between them, which is not a comparison).

1. On one GPU machine: `make up-gpu` (vLLM), run `inferbench` with `--engine vllm --hardware "<gpu>" --model <model>`
   and `--output vllm.json`; `make down`.
2. Same machine, same model: `make up-triton`, run the same scenario with `--engine triton-trtllm`
   and `--output triton.json`; `make down`.
3. `inferbench compare vllm.json triton.json` (it refuses mismatched or unlabeled runs).

Be aware of confounders you must control and report: quantization/precision, `max_batch_size` and in-flight
batching settings, engine build options, KV-cache sizing, tokenizer and chat template, and output length. The
Triton frontend's token accounting in streams is unverified (below), so prefer non-streaming for token
throughput comparisons until you confirm it.

## What has and has not been verified

Verified (on Apple Silicon, no GPU):

- The Triton backend against a **fake** OpenAI-compatible Triton server: it uses `/health/ready`, sends the
  configured model name, does not send `stream_options` by default, falls back gracefully if a server answers a
  streaming request with plain JSON, and shares one behavioural contract with the vLLM backend (tests fail if
  the health path or the `stream_options` default is changed).
- `tritonrepo-check` against generated fixtures, and its Go stage building and running in a container.
- The Dockerfile passes `docker build --check` (one intentional warning: the required build args have no
  default), the entrypoint passes `bash -n`, and the Compose and Helm configuration render and schema-validate.

**Not verified; needs an NVIDIA GPU and is left for the cloud phase:**

- That the Triton image builds with any specific `TRITON_TAG` / `TRITON_CLI_REF` pair, and that `triton import`,
  engine building, `triton start --frontend openai` and model loading work in it.
- That the Triton OpenAI frontend accepts streaming requests for TensorRT-LLM models, and whether it accepts
  `stream_options`. NVIDIA's page does not document either for TensorRT-LLM, so the backend defaults to *not*
  requesting usage in streams (`BACKEND_<NAME>_STREAM_USAGE=true` to opt in). Without it, streamed Triton
  responses carry no token counts and the gateway reports them as missing instead of guessing.
- Any GPU scheduling, performance, or the vLLM `v0.31.0` image's arguments.
- No Triton or TensorRT-LLM performance numbers exist yet. None are claimed.
