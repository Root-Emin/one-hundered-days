# syntax=docker/dockerfile:1
#
# The optimised build. Same program, ~100x smaller image, and rebuilds that
# take seconds instead of minutes.
#
# Build (from learning/go):
#   DOCKER_BUILDKIT=1 docker build \
#     -f Section-16-.../Day-77/dockerfiles/Dockerfile \
#     --build-arg VERSION=v1.0.0 \
#     --build-arg COMMIT=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t day77:optimised .

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

# 1. Dependency manifests first. This layer is invalidated only when go.mod or
#    go.sum change, so the download below is cached across source edits.
COPY go.mod go.sum ./

# 2. BuildKit cache mounts: the module cache and the build cache survive
#    BETWEEN builds, without being baked into a layer. This is the single
#    biggest win on a project with many dependencies.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# 3. Then the source.
COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

# TARGETOS/TARGETARCH are set by BuildKit for the target platform, which is
# what makes `docker buildx build --platform linux/amd64,linux/arm64` work
# from one Dockerfile.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# CGO_ENABLED=0: a static binary, so the runtime image needs no libc.
#
# -trimpath: no build machine paths in the binary. It also makes the build
# reproducible, which is what lets two builds of the same commit be compared.
#
# -ldflags "-s -w": drop the symbol table and DWARF info. ~30% smaller.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
      -o /out/api \
      ./Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-77/cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/api /api

USER nonroot:nonroot

EXPOSE 8080

ENV PORT=8080

ENTRYPOINT ["/api"]
