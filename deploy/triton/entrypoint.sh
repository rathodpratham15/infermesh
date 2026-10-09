#!/usr/bin/env bash
# Entrypoint for the InferMesh Triton image. Needs an NVIDIA GPU. Not run in this project's
# development environment.
#
#   1. If the model repository is empty and TRITON_IMPORT_MODEL is set, build a TensorRT-LLM engine
#      and generate the repository with `triton import` (slow, memory hungry, BETA tooling).
#   2. Sanity-check the repository (structure and template mistakes) before loading it.
#   3. Start Triton with its OpenAI-compatible frontend on :9000.
#
# Environment:
#   MODEL_REPO                 model repository path (default /models)
#   ENGINE_DEST_PATH           where triton-cli stores built engines (default /engines)
#   TRITON_IMPORT_MODEL        model to import when MODEL_REPO is empty, e.g. llama-3.1-8b-instruct
#   TRITON_TOKENIZER           HF id or path of the tokenizer used for chat templates (recommended)
#   TRITON_SKIP_REPO_CHECK=1   skip the repository check
#   TRITON_EXTRA_ARGS          extra arguments appended to `triton start`
set -euo pipefail

: "${MODEL_REPO:=/models}"
mkdir -p "$MODEL_REPO"

if [ -z "$(ls -A "$MODEL_REPO" 2>/dev/null)" ]; then
  if [ -z "${TRITON_IMPORT_MODEL:-}" ]; then
    echo "model repository $MODEL_REPO is empty and TRITON_IMPORT_MODEL is not set." >&2
    echo "mount a prepared repository at $MODEL_REPO, or set TRITON_IMPORT_MODEL to build one." >&2
    exit 1
  fi
  echo "importing $TRITON_IMPORT_MODEL (builds a TensorRT-LLM engine; this can take a long time)..."
  triton import -m "$TRITON_IMPORT_MODEL" --backend tensorrtllm --repo "$MODEL_REPO"
fi

if [ "${TRITON_SKIP_REPO_CHECK:-0}" != "1" ]; then
  echo "checking model repository..."
  tritonrepo-check "$MODEL_REPO"
fi

args=(start --repo "$MODEL_REPO" --frontend openai --mode local)
if [ -n "${TRITON_TOKENIZER:-}" ]; then
  args+=(--openai-chat-template-tokenizer "$TRITON_TOKENIZER")
fi
# shellcheck disable=SC2206  # intentional word splitting of user-provided extra arguments
args+=(${TRITON_EXTRA_ARGS:-})

echo "starting: triton ${args[*]}"
exec triton "${args[@]}"
