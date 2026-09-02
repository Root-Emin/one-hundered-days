# Day 55 — Secured Notes API

Section 11 capstone: the notes MVP with registration, login, JWT access
tokens, rotating refresh tokens, role-based authorization, input validation,
rate limiting and an audit trail.

## Running it

```bash
go run .                                  # :8080, in-memory DB, demo users seeded
DB_PATH=data/day55.db go run .            # persistent
JWT_SIGNING_KEY=$(openssl rand -base64 32) ENV=production go run .
```

Demo accounts (development only, seeded when `DB_PATH=:memory:`):

| email | role | password |
|---|---|---|
| `member@example.com` | member | `correct-horse-7` |
| `editor@example.com` | editor | `correct-horse-7` |
| `admin@example.com` | admin | `correct-horse-7` |

## Getting and sending a token

**1. Register** (new accounts are always `member`):

```bash
curl -XPOST localhost:8080/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","display_name":"Ada","password":"correct-horse-7"}'
```

Password policy: 12–128 characters, at least one letter and one digit, not a
well-known password.

**2. Log in** to get a token pair:

```bash
curl -XPOST localhost:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","password":"correct-horse-7"}'
```

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsImtpZCI6InYxIn0...",
  "refresh_token": "8Kq3...",
  "token_type": "Bearer",
  "expires_in": 900,
  "user": { "id": 4, "email": "ada@example.com", "role": "member", ... }
}
```

**3. Send the access token** on every protected request:

```bash
curl localhost:8080/notes -H "Authorization: Bearer $ACCESS_TOKEN"
```

**4. Refresh** when the access token expires (after 15 minutes). The refresh
token is single-use — store the new one:

```bash
curl -XPOST localhost:8080/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"'"$REFRESH_TOKEN"'"}'
```

**5. Log out** — revokes the access token immediately and drops every refresh
token for the user:

```bash
curl -XPOST localhost:8080/auth/logout -H "Authorization: Bearer $ACCESS_TOKEN"
```

### Client rules

- Send the access token as `Authorization: Bearer <token>`, nothing else.
- Treat both tokens as secrets: never in a URL, a log, or `localStorage` in a
  browser context (use an `HttpOnly` cookie or in-memory storage there).
- On `401` with `{"error":"token expired"}`, refresh once and retry. On any
  other `401`, send the user back to login.
- On `403`, do not retry: the identity is fine, the permission is not.
- On `429`, honour the `Retry-After` header.

### Status codes

| Code | Meaning |
|---|---|
| `400` | Malformed JSON, or an unknown field (`DisallowUnknownFields`) |
| `401` | No token, invalid token, expired token, or wrong credentials |
| `403` | Valid identity, insufficient role — or the account is suspended |
| `409` | Email already registered |
| `422` | Input failed validation |
| `429` | Rate limited (global, or the stricter login limiter) |

## Roles

| Permission | member | editor | admin |
|---|:--:|:--:|:--:|
| `note:create` | ✅ | ✅ | ✅ |
| read own note | ✅ | ✅ | ✅ |
| `note:read:any` | — | ✅ | ✅ |
| delete own note | ✅ | ✅ | ✅ |
| `note:delete:any` | — | — | ✅ |
| `user:list` | — | — | ✅ |
| `user:suspend` | — | — | ✅ |
| `audit:read` | — | — | ✅ |

Roles are assigned server side. `role` is not an accepted field on
registration, and ownership always comes from the token, never from the body.

## What protects what

| Control | Where | Threat |
|---|---|---|
| bcrypt password hashes | `auth.go` | Database leak → offline cracking |
| Constant-time dummy hash on unknown emails | `login` | User enumeration by timing |
| Identical error text for wrong password / unknown user | `login` | User enumeration by message |
| Login limiter (5 burst, then 1 per 6s, per email **and** IP) | `auth.go` | Brute force, credential stuffing |
| Global limiter (20/s, burst 40, per IP) | `Routes` | One client exhausting the service |
| HS256 with pinned algorithm, issuer, audience, expiry | `TokenService.Verify` | `alg:none`, forged and cross-service tokens |
| 15-minute access tokens | `AccessTokenTTL` | Bounded blast radius for a stolen token |
| Refresh tokens stored as SHA-256, single use, rotating | `store.go` | Token theft; replay revokes the family |
| User reloaded from the database on every request | `RequireAuth` | Suspension takes effect instantly |
| `jti` denylist on logout | `TokenService.Revoke` | Access token valid after logout |
| Allowlist validation, body cap, unknown fields rejected | `auth.go`, `decodeJSON` | Injection, oversized payloads, mass assignment |
| Security headers (`nosniff`, `DENY`, CSP, `no-store`) | `securityHeaders` | MIME sniffing, clickjacking, cached secrets |
| Audit log of every auth and admin action | `store.Audit` | Forensics after an incident |

## What is *not* solved

Written down honestly, because the gaps are as important as the controls:

1. **No TLS in this process.** It speaks plaintext HTTP and expects TLS
   termination in front of it (Day 54 has the hardened `tls.Config`). Deployed
   as-is on a public interface, every token is readable on the wire.
2. **The denylist is per-process and in memory.** With more than one replica,
   a logout only revokes the access token on the instance that handled it.
   The fix is shared state (Redis, Day 81) or accepting the 15-minute window.
3. **Rate limiting is per-process too.** Behind a load balancer the effective
   limit is multiplied by the replica count. Real protection belongs at the
   edge or in shared storage.
4. **No CSRF defence**, because this API is bearer-token only and sets no
   cookies. The moment a cookie-based browser flow is added, CSRF tokens or
   `SameSite=Strict` become mandatory.
5. **No email verification and no password reset.** Registration trusts the
   address, and a forgotten password is unrecoverable. Both flows are their
   own security surface (reset tokens must be single-use and short-lived).
6. **No MFA.** A stolen password is a full account takeover.
7. **No breach-list check on passwords.** The policy is length plus a tiny
   denylist; a real deployment should check Have I Been Pwned's k-anonymity
   range API.
8. **No key rotation schedule.** `JWT_SIGNING_KEY_ID` supports rotation and
   Day 52 has the multi-key verifier, but this build carries a single key and
   no rotation runbook.
9. **Audit log is append-only by convention, not by permission.** Anyone with
   database access can edit it; a real one ships to write-once storage.
10. **govulncheck is not wired into CI here.** It runs by hand (Day 54,
    `go run . audit`); Day 79 puts it in the pipeline.

## Tests

```bash
go test ./...
go test -race -count=1 ./...
go test -run TestRolePermissions -v ./...
```

The suite covers registration (valid, duplicate, weak password, bad email),
login (success, wrong password, unknown user, suspended, rate limited), tokens
(missing, malformed, tampered, expired, revoked, rotated, replayed) and
authorization (allowed role, forbidden role, ownership, privilege escalation
attempts). Every one of those is a regression somebody has shipped before.
