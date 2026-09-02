# Linkr — deployment runbook

For the person deploying, and for the person woken up afterwards. Every
procedure here has been run except the ones that need a registry, and those are
marked.

## Environment variables

The same image runs everywhere; only these change. An image that differs
between staging and production is not the thing you tested.

| Variable | Default | Notes |
|---|---|---|
| `LINKR_ADDR` | `:8096` | Public listener. |
| `LINKR_METRICS_ADDR` | `127.0.0.1:9096` | **Must be loopback in production** — `config.Validate` refuses otherwise. |
| `LINKR_DATABASE_URL` | `/data/linkr.db` | On the volume, never in the image layer. |
| `LINKR_BASE_URL` | `http://localhost:8096` | The origin printed in `short_url`. Wrong here means every returned link is wrong. |
| `LINKR_ENV` | `production` | Switches the log format to JSON and tightens validation. |
| `LINKR_LOG_LEVEL` | `info` | `debug` logs the probes too — noisy, occasionally necessary. |
| `LINKR_CACHE_TTL` | `60s` | The bound on how long a deactivated link can keep redirecting if an invalidation is missed. |
| `LINKR_RATE_LIMIT` | `120` | Requests per minute per API key. |
| `LINKR_SHUTDOWN_TIMEOUT` | `15s` | Must be **shorter** than the orchestrator's grace period. |

Everything is validated at startup: a malformed duration stops the process in
the first second rather than producing a zero timeout — which means *no*
timeout — that surfaces as a hang three hours later.

## Deploying

```sh
# 1. what will be deployed
docker pull ghcr.io/<owner>/linkr:v1.2.0
docker image inspect ghcr.io/<owner>/linkr:v1.2.0 --format '{{.Id}}'   # record this digest

# 2. staging first
docker compose -f docker-compose.yml up -d
curl -fsS localhost:8099/healthz          # the process is up
curl -fsS localhost:8099/readyz           # the database is usable

# 3. the smoke test, against staging
go run ./cmd/loadsmoke -url http://localhost:8099/<a-known-code> -requests 2000

# 4. production, then watch for one deploy cycle
```

**Migrations run automatically on startup**, before the listener opens — so the
service is never reachable with a schema it cannot use. They are idempotent:
starting twice applies nothing the second time.

A migration that fails stops the process. That is deliberate: a service running
against a half-migrated schema corrupts data quietly, and a service that
refuses to start does not.

## Verifying a deploy

```sh
curl -fsS localhost:8099/healthz    # liveness  — is the process up
curl -fsS localhost:8099/readyz     # readiness — are its dependencies usable
docker exec <container> /usr/local/bin/linkr -version
```

The first log line carries the version, commit and build time. When an incident
starts with "it broke at 14:32", the first question is which build was running.

Then watch, for one deploy cycle:

| Metric | What a problem looks like |
|---|---|
| `linkr_http_requests_total{class="5xx"}` | any sustained increase |
| `linkr_http_request_duration_seconds` p95 for `/{code}` | above 5 ms — the SLO |
| `linkr_redirect_cache_total{result="miss"}` | high right after a restart (expected, warms in seconds), sustained is not |
| `linkr_outbox_pending` | **rising and not falling** — the worker is dead or behind |
| `linkr_clicks_dropped_total` | anything above zero |

## Rolling back

```sh
# by DIGEST, not by tag: a tag can be moved, a digest cannot
docker run ... ghcr.io/<owner>/linkr@sha256:<digest-of-the-last-good-build>
```

**Before rolling back, check whether the release included a migration.**

- **No migration** → roll back freely. The old binary runs against the same
  schema.
- **An additive migration** (a new table, a new nullable column) → roll back
  freely. The old binary ignores what it does not know about. Every migration
  in Linkr so far is additive, by design.
- **A destructive migration** (a dropped column, a narrowed type) → **do not
  roll the schema back under a running service.** Roll the *binary* back only
  if it can still read the new schema; otherwise fix forward. `migrate down`
  exists for a controlled maintenance window, not for an incident.

That distinction is why every `.up.sql` has a `.down.sql` and why the store
refuses to load a migration without one — but having a rollback script is not
the same as it being safe to run at 3am.

## When something is wrong

| Symptom | Likely cause | What to do |
|---|---|---|
| `/readyz` fails, `/healthz` passes | The database is unreachable | The container is *not* restarted — that is correct. Cached redirects keep working. Check the volume mount. |
| Every redirect is 404 | Wrong `LINKR_DATABASE_URL`, or an empty volume | Compare the mount with the runbook; the database is on the volume, not in the image. |
| `short_url` values are wrong | `LINKR_BASE_URL` is wrong | It is cosmetic but embarrassing: fix and redeploy; existing links are unaffected. |
| 429s from a legitimate client | `LINKR_RATE_LIMIT` too low for them | Raise it, restart. Buckets are in memory and reset on restart. |
| `linkr_outbox_pending` climbing | The worker is dead, or the database is write-blocked | Restart. The events are durable rows; nothing is lost, and the aggregate catches up. |
| Clicks stopped counting, redirects fine | Same as above | Same. This is the failure mode the async design chooses on purpose. |
| The process will not start | Config validation failed | The error names **every** invalid field at once — read it, fix them together. |

## Backup and restore

The whole database is one file on the volume.

```sh
# back up - .backup is consistent while the service is running, cp is not
docker exec <container> sqlite3 /data/linkr.db ".backup /data/backup.db"
docker cp <container>:/data/backup.db ./linkr-$(date +%F).db

# restore
docker compose down
docker cp ./linkr-2026-09-02.db <container>:/data/linkr.db
docker compose up -d
```

Restoring loses every link created after the backup, and those codes may have
been shared. Restore is a last resort, not a rollback.

## What has actually been run

Honesty about what is verified, since Docker's daemon is not running on the
machine this was written on:

- ✅ The service itself, end to end: create, redirect, deactivate, stats,
  metrics, rate limiting, the worker, the load test.
- ✅ The Dockerfile, compose file and both workflows: parsed and asserted by
  `deploy/deploy_test.go` — non-root, pinned bases, exec-form entrypoint,
  reproducible flags, a volume for the database, an unpublished metrics port,
  timeouts on every job, pinned actions, and push restricted to `main`.
- ⚠️ `docker build` and `docker compose up`: **not run here.** The commands are
  in this runbook and in CI, and the artefacts they consume are checked by the
  tests above.
- ⚠️ Pushing to a registry and rolling back by digest: not run — it needs a
  registry and credentials.
