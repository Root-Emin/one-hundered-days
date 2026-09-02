# syntax=docker/dockerfile:1
#
# Production image for the Day 80 MVP.
#
#   cd learning/go
#   docker build -f Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/Dockerfile \
#     --build-arg VERSION=v0.1.0 \
#     --build-arg COMMIT=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t day80-api:v0.1.0 .

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV MODULE=example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w \
        -X ${MODULE}/internal/buildinfo.Version=${VERSION} \
        -X ${MODULE}/internal/buildinfo.Commit=${COMMIT} \
        -X ${MODULE}/internal/buildinfo.BuildTime=${BUILD_TIME}" \
      -o /out/api \
      ./Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=none

# OCI labels: the image carries its own provenance, so `docker inspect`
# answers "what is this and where did it come from?".
LABEL org.opencontainers.image.title="day80-api" \
      org.opencontainers.image.description="OneHundredDay Day 80 - deployable Go MVP" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /out/api /api

USER nonroot:nonroot

EXPOSE 8080

ENV PORT=8080 \
    LOG_FORMAT=json \
    SHUTDOWN_TIMEOUT=15s \
    DRAIN_DELAY=2s

# The binary probes itself: there is no shell or curl in a distroless image.
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
