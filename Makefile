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
.PHONY: up up-host-model up-cpu-model up-gpu down logs images

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

down: ## stop the stack (keeps volumes)
	docker compose --profile cpu-model --profile gpu down

logs:
	docker compose logs -f --tail=100
