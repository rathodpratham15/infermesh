# NVIDIA Triton Inference Server + TensorRT-LLM, served through Triton's OpenAI-compatible frontend.
#
# *** REQUIRES AN NVIDIA GPU TO RUN. ***  The image can be BUILT on any machine with Docker (a
# linux/amd64 build host or emulation is needed; the NVIDIA base image is large), but the engine build
# and model serving only work on an NVIDIA GPU. This image has never been run in this project's
# development environment (Apple Silicon, no NVIDIA GPU). See docs/gpu-deployment.md.
#
# Both build args are REQUIRED and deliberately have no defaults, because the Triton container release
# and the triton-cli version must be a compatible pair, and that pairing is NVIDIA's to define:
#   TRITON_TAG      Triton container release, e.g. 25.02 -> nvcr.io/nvidia/tritonserver:25.02-trtllm-python-py3
#   TRITON_CLI_REF  triton_cli git ref matching that release (see the compatibility table in
#                   https://github.com/triton-inference-server/triton_cli). The only pair documented there
#                   at the time of writing is TRITON_TAG=25.02 with TRITON_CLI_REF=0.1.3; newer containers
#                   are not listed, so verify a newer pair yourself before relying on it.
#
#   docker build -f deploy/triton/Dockerfile --build-arg TRITON_TAG=25.02 --build-arg TRITON_CLI_REF=0.1.3 \
#     -t infermesh-triton:25.02 .

# Build args used in FROM lines must be declared before the first FROM.
ARG GO_VERSION=1.27
ARG TRITON_TAG
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS check
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/tritonrepo-check ./cmd/tritonrepo-check
COPY internal/tritonrepo ./internal/tritonrepo
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /out/tritonrepo-check ./cmd/tritonrepo-check

FROM nvcr.io/nvidia/tritonserver:${TRITON_TAG}-trtllm-python-py3
ARG TRITON_TAG
ARG TRITON_CLI_REF
RUN test -n "${TRITON_TAG}" && test -n "${TRITON_CLI_REF}" || \
    (echo "build with --build-arg TRITON_TAG=<release> --build-arg TRITON_CLI_REF=<triton_cli ref>" >&2; exit 1)
RUN pip install --no-cache-dir "git+https://github.com/triton-inference-server/triton_cli.git@${TRITON_CLI_REF}"

COPY --from=check /out/tritonrepo-check /usr/local/bin/tritonrepo-check
COPY deploy/triton/entrypoint.sh /usr/local/bin/infermesh-triton-entrypoint
RUN chmod +x /usr/local/bin/infermesh-triton-entrypoint

# Engines are kept outside the model repository so they can be cached on a volume and reused.
ENV MODEL_REPO=/models \
    ENGINE_DEST_PATH=/engines
EXPOSE 9000
ENTRYPOINT ["/usr/local/bin/infermesh-triton-entrypoint"]
