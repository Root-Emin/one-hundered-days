# ADR 0002 — Random base62 codes, not sequential ids

**Status:** accepted · **Date:** 2026-09-02

## Context

Every short link needs a code. The obvious choice is the row id, base62
encoded: no collisions, no lookups, shortest possible codes.

## Decision

Seven random base62 characters, generated with `crypto/rand`, with a uniqueness
check on insert and a bounded retry.

## Why

Sequential ids leak two things:

- **Volume.** Anyone can watch the codes advance and know exactly how many
  links were created between Tuesday and Friday.
- **Everyone else's links.** `/abc124` is the link created after `/abc123`.
  Enumerating the entire corpus costs one loop, and short links are routinely
  used for documents whose only protection is that the URL is unguessable.

Seven base62 characters is 62⁷ ≈ 3.5 × 10¹², which at a million links gives a
collision probability per insert of roughly 3 × 10⁻⁷ — rare enough that the
retry loop is a formality, cheap enough that keeping it is free.

## Consequences

- Codes are one character longer than they would need to be. Acceptable.
- Insert does a uniqueness check, so link creation is not a blind write. It is
  not the hot path.
- Custom codes are allowed, validated against the same alphabet, and must not
  collide with a generated one — same table, same constraint.

## Revisit when

Never, realistically. If codes must get shorter, the answer is a larger
alphabet, not sequential ids.
