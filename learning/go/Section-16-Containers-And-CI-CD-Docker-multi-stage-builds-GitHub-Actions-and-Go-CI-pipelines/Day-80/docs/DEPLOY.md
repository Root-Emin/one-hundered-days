# Deploying day80-api

Everything an operator needs, on one page. If something here is out of date,
that is a bug.

## What this is

A stateless HTTP service. It holds notes in memory, so instances are
interchangeable and any of them can be restarted at any time — with the
caveat in [State](#state) below.

| | |
|---|---|
| Image | `ghcr.io/<org>/<repo>/day80-api:<version>` |
| Port | 8080 (HTTP) |
| Liveness | `GET /healthz` |
| Readiness | `GET /readyz` |
| Metrics | `GET /metrics` (Prometheus text format) |
| Version | `GET /version`, or `docker run --rm IMAGE -version` |
| User | `nonroot` (uid 65532) |
| Filesystem | read-only; the service writes nothing to disk |

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Listen port |
| `LOG_FORMAT` | `json` | `json` for production, `text` for a terminal |
| `LOG_LEVEL` | `info` | `debug` while investigating; noisy and expensive |
| `SHUTDOWN_TIMEOUT` | `15s` | How long in-flight requests get to finish |
| `DRAIN_DELAY` | `2s` | Wait after failing readiness, before refusing connections |

**`DRAIN_DELAY` must be longer than the load balancer's readiness interval**,
or the balancer is still routing traffic when the service stops accepting it.
For a Kubernetes readiness probe every 5s, use `DRAIN_DELAY=10s` and a
`terminationGracePeriodSeconds` above `DRAIN_DELAY + SHUTDOWN_TIMEOUT`.

## Running it

### Docker

```bash
docker run -d --name day80-api \
  -p 8080:8080 \
  -e LOG_FORMAT=json \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --memory 128m --cpus 0.5 \
  --restart unless-stopped \
  ghcr.io/<org>/<repo>/day80-api:v0.1.0
```

### Compose (the local stack, with Prometheus)

```bash
cd learning/go
docker compose -f Section-16-.../Day-80/docker-compose.yml up --build
# API        http://localhost:8080
# Prometheus http://localhost:9090
```

### Kubernetes sketch

```yaml
spec:
  terminationGracePeriodSeconds: 30       # > DRAIN_DELAY + SHUTDOWN_TIMEOUT
  containers:
    - name: api
      image: ghcr.io/<org>/<repo>/day80-api:v0.1.0
      ports: [{containerPort: 8080}]
      env:
        - {name: DRAIN_DELAY, value: "10s"}
      livenessProbe:                       # process alive?
        httpGet: {path: /healthz, port: 8080}
        periodSeconds: 10
        failureThreshold: 3
      readinessProbe:                      # send it traffic?
        httpGet: {path: /readyz, port: 8080}
        periodSeconds: 5
        failureThreshold: 2
      resources:
        requests: {memory: 32Mi, cpu: 50m}
        limits:   {memory: 128Mi}
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        readOnlyRootFilesystem: true
        allowPrivilegeEscalation: false
        capabilities: {drop: [ALL]}
```

The two probes are different questions, and mixing them up is a classic
outage: a liveness probe that checks a dependency restarts every pod during a
dependency blip.

## Releasing

```bash
make ci                       # everything the pipeline runs
make tag VERSION=v0.1.0       # annotated tag
git push origin v0.1.0        # triggers .github/workflows/release.yml
```

The release workflow re-runs vet and tests, then produces:

| Artifact | Where |
|---|---|
| `api-linux-amd64`, `api-linux-arm64`, `api-darwin-arm64` | GitHub release |
| `checksums.txt` (SHA-256) | GitHub release |
| Multi-arch image, tagged `v0.1.0`, `0.1`, `sha-<short>` | ghcr.io |
| SBOM and provenance attestation | attached to the image |

The binary reports what it is:

```bash
./api-linux-amd64 -version
# version   v0.1.0
# commit    3d60c3c
# built     2026-09-01T18:31:01Z
```

## Verifying a deployment

```bash
curl -s $HOST/healthz              # {"status":"ok"}
curl -s $HOST/readyz               # {"status":"ready"}
curl -s $HOST/version | jq .version
curl -s $HOST/metrics | grep app_build_info
```

`app_build_info{version="v0.1.0",...} 1` is how a dashboard shows which
version each instance is running — invaluable during a rolling deploy.

## Rolling back

Images are immutable and tagged by version, so a rollback is a re-deploy of
the previous tag:

```bash
docker service update --image ghcr.io/<org>/<repo>/day80-api:v0.0.9 day80-api
# or
kubectl rollout undo deployment/day80-api
```

Never re-tag an existing version. `v0.1.0` must mean one image, forever, or
"which version is running?" stops having an answer.

## State

Notes are in memory. Consequences, stated plainly:

- A restart loses them.
- Two replicas do not see each other's notes.

That is acceptable for this teaching MVP and unacceptable for a real service.
The fix is the database layer from Sections 9–10, at which point this document
gains a section on migrations and connection limits.

## Troubleshooting

| Symptom | Check |
|---|---|
| Container exits immediately | `docker logs day80-api` — usually a port already in use |
| Health check failing | `docker exec day80-api /api -healthcheck` (distroless has no shell) |
| Deploys drop requests | `DRAIN_DELAY` shorter than the LB's readiness interval |
| Container killed during shutdown | grace period shorter than `DRAIN_DELAY + SHUTDOWN_TIMEOUT` |
| "which version is this?" | `GET /version`, or `docker inspect` the OCI labels |
