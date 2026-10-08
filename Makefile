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
