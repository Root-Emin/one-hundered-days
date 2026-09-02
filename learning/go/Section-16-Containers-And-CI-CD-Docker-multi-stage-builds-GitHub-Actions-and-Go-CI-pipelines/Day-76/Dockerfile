# syntax=docker/dockerfile:1
#
# Multi-stage build for a Go service.
#
# Build from learning/go (the module root), not from this directory, because
# the build needs go.mod:
#
#   cd learning/go
#   docker build -f Section-16-.../Day-76/Dockerfile -t day76-api:dev .

# ---------------------------------------------------------------------------
# Stage 1: build
#
# The toolchain image is ~800 MB. None of it ships: only the compiled binary
# is copied into the runtime stage, which is what "multi-stage" buys.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

# git is needed if any dependency is fetched from a VCS.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Dependencies first, in their own layer. This layer is rebuilt only when
# go.mod or go.sum change - not on every source edit. Day 77 measures what
# that saves.
COPY go.mod go.sum ./
RUN go mod download

# Then the source.
COPY . .

# Build arguments, passed through to the binary as version metadata:
#   docker build --build-arg VERSION=v1.0.0 --build-arg COMMIT=$(git rev-parse --short HEAD) .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# CGO_ENABLED=0 produces a static binary with no libc dependency, which is
# what makes a distroless or scratch runtime image possible at all.
#
# -trimpath removes local filesystem paths from the binary: smaller, and it
# stops build machine paths leaking into stack traces.
#
# -ldflags "-s -w" strips the symbol table and DWARF debug info. It shrinks
# the binary by roughly a third; the cost is that a core dump is harder to
# read, which is why some teams keep symbols and accept the size.
ENV CGO_ENABLED=0

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildTime=${BUILD_TIME}" \
      -o /out/api \
      ./Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-76/cmd/api

# ---------------------------------------------------------------------------
# Stage 2: runtime
#
# distroless/static contains a CA bundle, /etc/passwd and timezone data - and
# no shell, no package manager, no busybox. An attacker who gets code
# execution finds nothing to execute.
#
# The alternatives:
#   scratch          smallest, but no CA certificates and no timezone data
#   distroless/static this: ~2 MB, TLS and timezones work
#   alpine           ~8 MB, has a shell - convenient to debug, larger surface
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Labels belong on the image, not in a wiki.
LABEL org.opencontainers.image.title="day76-api" \
      org.opencontainers.image.description="Containerised Go API - OneHundredDay Day 76" \
      org.opencontainers.image.source="https://github.com/masterfabric/one-hundered-days" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /

COPY --from=builder /out/api /api

# nonroot is uid 65532 in the distroless images. Running as root inside a
# container is the default nobody should accept: a container escape then
# starts from root.
USER nonroot:nonroot

EXPOSE 8080

ENV PORT=8080 \
    SHUTDOWN_TIMEOUT=15s

# ENTRYPOINT in exec form (a JSON array), NOT shell form.
#
#   ENTRYPOINT ["/api"]     -> the binary is PID 1 and receives SIGTERM
#   ENTRYPOINT /api         -> /bin/sh -c "/api", the shell is PID 1, and it
#                              does not forward signals: every deploy becomes
#                              a SIGKILL after the grace period
#
# distroless has no shell, so the shell form would not even start - which is
# a feature.
ENTRYPOINT ["/api"]
