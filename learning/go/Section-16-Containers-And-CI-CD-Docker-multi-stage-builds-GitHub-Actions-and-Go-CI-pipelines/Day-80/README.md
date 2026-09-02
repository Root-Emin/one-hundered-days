# Day 80 — Shipping the MVP

Section 16 capstone: the service builds in CI, runs in a container, and ships
with the documents an operator needs.

Operator documentation lives in [docs/DEPLOY.md](docs/DEPLOY.md).

## The path from laptop to running container

```bash
make ci                       # lint + test + build, exactly what CI runs
make build                    # a versioned binary for this machine
./dist/api -version           # it knows what it is

make docker-build             # multi-stage image, ~8 MB
make docker-run               # run it

make compose-up               # api + prometheus, one command

make tag VERSION=v0.1.0       # annotated tag
git push origin v0.1.0        # release.yml builds and publishes
```

## What ships

| Artifact | Produced by | Contains |
|---|---|---|
| `api-linux-amd64`, `api-linux-arm64`, `api-darwin-arm64` | `make release` / release.yml | Static binaries, symbols stripped, version injected |
| `checksums.txt` | `make release` | SHA-256 of each binary |
| `ghcr.io/…/day80-api:v0.1.0` | release.yml | Multi-arch distroless image, non-root |
| SBOM + provenance | `docker/build-push-action` | What is in the image and how it was built |

Every artifact is built from one commit, with `-ldflags` carrying the version
and the commit into the binary. `GET /version` and `app_build_info` in
`/metrics` both report it, so "which version is running?" is answerable from a
terminal and from a dashboard.

## The pipeline

`ci.yml` on every push and pull request:

```
lint ────────┐
test  ───────┼──► build ──► image
integration ─┘
```

`release.yml` on a `v*` tag:

```
verify ──┬──► binaries  (GitHub release + checksums)
         └──► image     (ghcr.io, multi-arch, scanned)
```

The release re-runs vet and the tests before publishing anything: a tag is not
a promise that CI passed on that commit, it is just a name.

## Image

| Property | Value | Why |
|---|---|---|
| Base | `gcr.io/distroless/static-debian12:nonroot` | ~2 MB, no shell, no package manager |
| Size | ~8 MB | Binary plus base |
| User | `nonroot` (65532) | A container escape must not begin as root |
| Filesystem | read-only | The service writes nothing |
| Capabilities | all dropped | An HTTP service needs none |
| Entrypoint | `["/api"]`, exec form | The binary is PID 1 and receives SIGTERM |
| Health check | `/api -healthcheck` | The binary probes itself; there is no curl |

## Graceful shutdown, in order

1. `SIGTERM` arrives at PID 1 — the binary, because the entrypoint is exec form.
2. `/readyz` starts returning 503. The load balancer notices and stops routing.
3. `DRAIN_DELAY` (2s) passes, so requests already routed still arrive.
4. `server.Shutdown` stops accepting and waits up to `SHUTDOWN_TIMEOUT` (15s)
   for in-flight requests.
5. The process exits.

`stop_grace_period` (25s) is longer than `DRAIN_DELAY + SHUTDOWN_TIMEOUT`
(17s), so the drain finishes before SIGKILL. `deploy_test.go` asserts that
inequality — getting it wrong drops requests on every deploy, silently.

## Versioning

Semantic versioning on annotated tags:

| Change | Bump |
|---|---|
| Breaking API change | major — `v2.0.0` |
| New endpoint or field | minor — `v1.1.0` |
| Fix, no contract change | patch — `v1.0.1` |

An image tag is immutable: `v0.1.0` means one image, forever. Re-tagging is
how a rollback stops being possible.

## Tests

```bash
go test ./...
```

Beyond the API tests, this day tests the *artifacts*:

- `Dockerfile` is multi-stage, static, non-root, exec-form, with cache mounts
- `stop_grace_period > DRAIN_DELAY + SHUTDOWN_TIMEOUT`
- the compose stack has both services and a scrape config
- `release.yml` runs vet and tests before publishing, and both publish jobs
  wait for it
- the Makefile injects the version, and `docs/DEPLOY.md` answers the operator
  questions

A Dockerfile is code. It regresses like code, and the failures are found in
production unless something checks them earlier.
