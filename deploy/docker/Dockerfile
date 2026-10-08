# One multi-stage Dockerfile for every Go binary in this repo.
#
#   docker build -f deploy/docker/Dockerfile --build-arg CMD=gateway -t infermesh-gateway .
#   docker build -f deploy/docker/Dockerfile --build-arg CMD=telemetry-consumer -t infermesh-telemetry-consumer .
#   docker build -f deploy/docker/Dockerfile --build-arg CMD=inferbench -t infermesh-inferbench .
#
# The build runs on the build platform and cross-compiles, so building for linux/amd64 on an
# Apple Silicon Mac (or the reverse) does not use emulation.

ARG GO_VERSION=1.27
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG CMD
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Dependencies first, so source edits do not invalidate the module download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test -n "$CMD" || (echo "build with --build-arg CMD=<gateway|telemetry-consumer|inferbench>" >&2; exit 1); \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/app ./cmd/${CMD}

# Static binary, no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
