# ADR 0004 — Hashed API keys, not sessions or JWTs

**Status:** accepted · **Date:** 2026-09-02

## Context

`/api/*` needs authentication. The clients are programs — scripts, CI jobs,
other services — not humans in browsers.

## Decision

Bearer API keys. Generated with `crypto/rand`, shown once at creation, stored
as a SHA-256 hash.

## Why

- **No sessions**, because there is no browser and nothing to keep logged in.
- **No JWTs.** A JWT buys stateless verification, which matters when the token
  is checked by many services that cannot reach a shared store. Here there is
  one service and one database; the lookup is a primary-key read. What a JWT
  would cost is revocation: a leaked key must stop working *now*, and a
  self-contained token cannot without the denylist that makes it stateful
  again.
- **Hashed at rest**, so a leaked database gives an attacker hashes rather than
  working credentials. SHA-256 rather than bcrypt because the input is 256 bits
  of entropy from `crypto/rand` — brute force is not the threat, and the check
  is on the hot path of every API call.

## Consequences

- **The key is unrecoverable.** Lost means rotate, which is the correct
  behaviour and must be said clearly at creation time.
- The comparison uses `subtle.ConstantTimeCompare`; a timing side channel on a
  hash lookup is unlikely to be exploitable, and the constant-time version is
  free.
- `last_used_at` is updated lazily — not on every request, which would turn
  every read into a write.

## Revisit when

Humans need accounts, or a second service needs to verify credentials without
reaching this database.
