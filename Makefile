.PHONY: build test lint fmt run

build:
	go build -o bin/ ./cmd/...

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

run:
	go run ./cmd/gateway

# --- local model (Apple Silicon, no NVIDIA GPU) ---
LOCAL_MODEL ?= mlx-community/Qwen2.5-0.5B-Instruct-4bit
LOCAL_MODEL_PORT ?= 8000

.PHONY: local-model run-vllm
local-model: ## serve a small OpenAI-compatible model with mlx-lm on :8000
	test -d .venv-local || python3 -m venv .venv-local
	.venv-local/bin/pip install -q mlx-lm
	.venv-local/bin/python -m mlx_lm.server --model $(LOCAL_MODEL) --port $(LOCAL_MODEL_PORT)

run-vllm: ## gateway pointed at the local model
	BACKENDS=vllm BACKEND_VLLM_URL=http://127.0.0.1:$(LOCAL_MODEL_PORT) BACKEND_VLLM_MODEL=$(LOCAL_MODEL) go run ./cmd/gateway

# --- Docker Compose (see docker-compose.yml) ---
.PHONY: up up-host-model up-cpu-model up-gpu up-triton triton-check down logs images

images: ## build the container images
	docker compose build

up: ## full stack with the mock backend (no model, no GPU)
	docker compose up -d --build

up-host-model: ## route the gateway to a model served on the host (run `make local-model` first)
	BACKENDS=vllm BACKEND_VLLM_URL=http://host.docker.internal:$(LOCAL_MODEL_PORT) BACKEND_VLLM_MODEL=$(LOCAL_MODEL) \
	  docker compose up -d --build

up-cpu-model: ## add a llama.cpp container as the engine (CPU only)
	BACKENDS=vllm BACKEND_VLLM_URL=http://llamacpp:8000 \
	  docker compose --profile cpu-model up -d --build

up-gpu: ## add vLLM in a container; REQUIRES an NVIDIA GPU + NVIDIA Container Toolkit
	BACKENDS=vllm BACKEND_VLLM_URL=http://vllm:8000 BACKEND_VLLM_MODEL=$${VLLM_MODEL:-Qwen/Qwen2.5-0.5B-Instruct} \
	  docker compose --profile gpu up -d --build

up-triton: ## add Triton + TensorRT-LLM; REQUIRES an NVIDIA GPU; set TRITON_TAG, TRITON_CLI_REF, TRITON_MODEL_NAME
	@test -n "$(TRITON_TAG)" && test -n "$(TRITON_CLI_REF)" && test -n "$(TRITON_MODEL_NAME)" || \
	  { echo "set TRITON_TAG, TRITON_CLI_REF and TRITON_MODEL_NAME (see docs/gpu-deployment.md)"; exit 1; }
	BACKENDS=triton BACKEND_TRITON_URL=http://triton:9000 BACKEND_TRITON_MODEL=$(TRITON_MODEL_NAME) \
	  docker compose --profile triton up -d --build

triton-check: ## sanity-check a Triton model repository: make triton-check REPO=/path/to/model_repo
	@test -n "$(REPO)" || { echo "usage: make triton-check REPO=/path/to/model_repo"; exit 1; }
	go run ./cmd/tritonrepo-check $(REPO)

down: ## stop the stack (keeps volumes)
	docker compose --profile cpu-model --profile gpu --profile triton down

logs:
	docker compose logs -f --tail=100

# --- Helm / Kubernetes (see docs/kubernetes.md) ---
CHART := deploy/helm/infermesh
.PHONY: helm-lint helm-validate

helm-lint: ## lint the chart with each values file
	helm lint $(CHART)
	helm lint $(CHART) -f $(CHART)/values-local.yaml
	helm lint $(CHART) -f $(CHART)/values-gpu.yaml

helm-validate: ## render the chart and validate it against Kubernetes schemas (needs kubeconform)
	helm template rel $(CHART) | kubeconform -strict -ignore-missing-schemas -summary -kubernetes-version 1.31.0
	helm template rel $(CHART) -f $(CHART)/values-local.yaml | kubeconform -strict -ignore-missing-schemas -summary -kubernetes-version 1.31.0
	helm template rel $(CHART) -f $(CHART)/values-gpu.yaml | kubeconform -strict -ignore-missing-schemas -summary -kubernetes-version 1.31.0
