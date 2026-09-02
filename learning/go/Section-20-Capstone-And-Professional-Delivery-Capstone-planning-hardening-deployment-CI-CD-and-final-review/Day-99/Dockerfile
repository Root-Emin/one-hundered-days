# Linkr's production image.
#
# Two stages: one that has a compiler, and one that does not. The shipped image
# contains a static binary and nothing else - no shell, no package manager, no
# source, no module cache. That is a smaller download and a much smaller attack
# surface: a remote code execution needs something to execute, and there is
# nothing here.

# ---- build ------------------------------------------------------------------

# Pinned to a patch version, not 1.26 or latest. Day 98's govulncheck found
# five standard-library vulnerabilities fixed in 1.26.6, and a floating tag is
# how a rebuild silently changes which of them you have.
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build

# TARGETOS/TARGETARCH are set by buildx for the target platform, so one
# Dockerfile cross-compiles for amd64 and arm64 from either.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG COMMIT_TIME=unknown

WORKDIR /src

# Dependencies first, in their own layer. Source changes far more often than
# go.mod does, so this layer is a cache hit on nearly every build - which is
# the difference between a 20-second build and a 3-minute one.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# CGO_ENABLED=0 gives a static binary with no libc dependency, which is what
# lets it run in an image that contains nothing.
#
# -trimpath removes absolute build paths, and the stamped time is the COMMIT
# time rather than the build time - both required for the build to be
# reproducible (Day 94).
ENV CGO_ENABLED=0

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildTime=${COMMIT_TIME}" \
      -o /out/linkr \
      ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/cmd/linkr

# ---- runtime ----------------------------------------------------------------

# distroless/static: libc-free, shell-free, ~2 MB. "nonroot" is the tag that
# ships a non-root user, which is the difference between a container escape
# landing as uid 0 and landing as nobody.
FROM gcr.io/distroless/static-debian12:nonroot

# The database lives on a volume, not in the image layer: a container's
# filesystem is discarded on restart, and a link shortener that forgets its
# links is not a link shortener.
WORKDIR /data

COPY --from=build --chown=nonroot:nonroot /out/linkr /usr/local/bin/linkr

USER nonroot:nonroot

EXPOSE 8096 9096

# Configuration by environment, not by flags baked into the image: the same
# image must run in staging and production, and an image that differs between
# them is not the thing you tested.
ENV LINKR_ADDR=":8096" \
    LINKR_METRICS_ADDR="127.0.0.1:9096" \
    LINKR_DATABASE_URL="/data/linkr.db" \
    LINKR_ENV="production" \
    LINKR_LOG_LEVEL="info"

# Exec form, not shell form. With the shell form the process is a child of
# /bin/sh, which does not forward SIGTERM - so every deploy would be a 30-second
# wait followed by SIGKILL, and every in-flight request would be cut off.
# (distroless has no shell anyway, which is a second reason.)
ENTRYPOINT ["/usr/local/bin/linkr"]
