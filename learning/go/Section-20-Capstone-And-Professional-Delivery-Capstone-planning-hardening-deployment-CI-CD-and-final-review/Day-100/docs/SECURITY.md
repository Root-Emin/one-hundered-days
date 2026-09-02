# Linkr — security review

A pass over the whole service, done before anyone else reads the code rather
than after. Each item says what was checked, what was found, and what the
service does about it.

## 1. Authentication

| Check | Finding |
|---|---|
| Where are credentials accepted? | `Authorization: Bearer` only. A key in a query string reaches access logs, `Referer` headers sent to third parties, and browser history — three places nobody audits. `TestQueryParameterIsNotACredential` asserts it is rejected. |
| How are keys stored? | SHA-256 hash. A leaked database gives an attacker hashes, not working credentials. The plaintext is printed once at creation and never persisted or logged. |
| Why not bcrypt? | The input is 256 bits from `crypto/rand`, so brute force is not the threat, and this check runs on every API request. bcrypt is for passwords, which humans choose badly. ([ADR 0004](adr/0004-api-keys.md)) |
| Comparison | `subtle.ConstantTimeCompare`. A timing channel on a hash lookup is unlikely to be exploitable and the constant-time version is free, which makes the leaky one hard to justify. |
| Can the middleware be bypassed? | It writes a 401 and returns; it never falls through. `TestMiddlewareStopsUnauthenticatedRequests` asserts the handler does not run. |
| Can an identity be forged? | The context key is an unexported type, so no other package can write to it. |

## 2. Authorization

| Check | Finding |
|---|---|
| Can one owner read another's links? | No. `GetLink` compares the owner and returns **404, not 403** — "this exists but is not yours" is an enumeration oracle. |
| Can one owner deactivate another's link? | No: the `UPDATE` is scoped by owner, and zero rows affected becomes `ErrNotFound`. Asserted in `TestDeactivateRefusesAnotherOwnersLink`. |
| Is the redirect authorized? | Deliberately not. A short link that needs a bearer token is not a short link. It is the one public route, and it is mounted outside the auth middleware rather than inside it with an exception. |

## 3. Input validation

| Input | Rule | Why |
|---|---|---|
| `target` | `http`/`https` only, absolute, host required, ≤ 2048 bytes | A `javascript:` or `data:` target turns every link into stored XSS **delivered from your domain**. This is the single most important check in the service. |
| `code` | base62 only, ≤ 32 characters, reserved words rejected | Rejected in the domain rather than by route precedence, so the rule survives someone adding a route later. |
| `expires_at` | RFC 3339, must be in the future | A link born expired means a 410 with no explanation. |
| Request body | 64 KiB cap, `DisallowUnknownFields` | Without the cap a client streams gigabytes into a handler expecting 100 bytes. A silently ignored misspelled field is a very expensive silence. |
| `limit`, `days` | bounded ranges | An unbounded `limit` is a full table scan on request. |

SQL is parameterised everywhere. The one place a query is built with
`fmt.Sprintf` — the `IN (?,?,?)` list in `ApplyClickBatches` — interpolates
**placeholders only**, never values.

## 4. Secrets and data in logs

| Check | Finding |
|---|---|
| Is the API key ever logged? | No. The request logger records method, path, status, bytes and duration — **not** the query string, not the `Authorization` header, not the body. |
| Do internal errors reach the client? | No. The handler logs the error with the request id and returns the id plus a generic message. An error string can carry a table name, a file path, or a row's contents. |
| Does a panic leak a stack? | No. `TestRecoverTurnsAPanicIntoAResponse` asserts the stack goes to the log and the response carries only the request id. |
| Is anything secret in the config dump? | Not today, and `Config.String` lists fields explicitly rather than reflecting over the struct — so adding a password field will not silently print it. |

## 5. Denial of service

| Vector | Mitigation |
|---|---|
| Slowloris (headers sent one byte at a time) | `ReadHeaderTimeout`, and `config.Validate` **refuses a zero value** — because zero means *no* timeout. |
| Unbounded request body | `http.MaxBytesReader`, 64 KiB. |
| A client's retry loop | Per-key token bucket, 429 with `Retry-After`. Keyed by the authenticated owner, not by IP: keying by IP punishes everyone behind one NAT and does nothing to a client with a thousand addresses. |
| Crawler hammering unknown codes | Negative cache entries, so those requests never reach the database. |
| Cache as a memory leak | `MaxEntries` with eviction; `TestEvictionBoundsMemory`. |
| Rate limiter as a memory leak | Idle buckets swept after an hour; `TestIdleBucketsAreSweptAway`. |
| A hot link serialising writes | Clicks are aggregated asynchronously and in batches; the redirect never writes. |

## 6. Exposure

| Check | Finding |
|---|---|
| Is `/metrics` public? | No — a separate listener on `127.0.0.1`, and `config.Validate` **refuses a non-loopback metrics address in production**. `/metrics` tells an attacker your traffic shape, error rate and deployment size for free. |
| Is `/debug/pprof` mounted? | Not in this service. If it is added, it goes on the same private listener — it exposes the heap and lets anyone start a 30-second CPU profile. |
| Does an error distinguish "missing" from "not yours"? | No, by design. See §2. |

## 7. Dependency scan

```
govulncheck ./...
```

**Result (2026-09-02, Go 1.26.5):** five standard-library vulnerabilities, all
fixed in **Go 1.26.6**:

| ID | Package | Issue |
|---|---|---|
| GO-2026-6218 | `net/url` | quadratic complexity in `resolvePath` |
| GO-2026-6090 | `crypto/tls` | unbounded post-handshake messages |
| GO-2026-6089 | `net/http` | `ReadHeaderTimeout` not applied on the unencrypted HTTP/2 check |
| GO-2026-5972 | `encoding/asn1` | unbounded recursion depth |
| GO-2026-5026 | `net/http` (`x/net/idna`) | ASCII-only Punycode labels not rejected |

**Action: upgrade the toolchain to 1.26.6.** Three of the five are reachable
from this service's own call graph — `net/http.Server.Serve` is in the trace
for two of them. None is exploitable in a way this code can mitigate; they are
fixed by rebuilding on a patched toolchain, which is why the Dockerfile pins
the Go version explicitly (Day 99) and CI runs `govulncheck` on every pull
request.

Two further vulnerabilities exist in required modules that this code does not
call. They are not urgent, and they are not ignored: `go mod tidy` plus a
scheduled scan is what keeps that true.

## What this review does not cover

Stated so the gaps are decisions:

- **No TLS termination here.** The service speaks HTTP behind a proxy. If that
  stops being true it needs a TLS configuration, not a flag.
- **No audit log.** Who created or deactivated which link is not recorded
  beyond the owner column.
- **No key rotation flow.** Issuing a new key works; revoking an old one is a
  `DELETE` nobody has written a command for yet.
- **No abuse detection.** Nothing stops a valid key shortening links to
  malware. A real link shortener needs a blocklist and a report endpoint;
  this one has the rate limit and nothing else.

Those four are in [GAPS.md](GAPS.md) with triggers, not left implicit.
