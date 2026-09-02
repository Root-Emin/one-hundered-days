# ADR 0003 — Clicks are recorded asynchronously

**Status:** accepted · **Date:** 2026-09-02

## Context

Every redirect should be counted. The simplest implementation increments a
counter in the same request that serves the redirect.

## Decision

The redirect writes a click event to the outbox and returns. A worker
aggregates events into `click_daily`.

## Why

- **The redirect is the latency-critical path.** A human is waiting. Adding a
  write to a read is adding the slowest thing in the request to the fastest.
- **A counter update is a write lock on a hot row.** Two thousand redirects a
  second on the same popular link serialise on it, and SQLite has one writer.
- **The stats endpoint must not scan `clicks`.** Aggregating in the worker
  means the read is a single indexed lookup, however many clicks exist.

## Consequences

- **Stats are eventually consistent**, by the worker's poll interval — seconds.
  The requirements say so, and no user-visible promise depends on them being
  immediate.
- **At-least-once delivery.** The worker may see an event twice, so
  aggregation is keyed by event id and idempotent (Day 84's pattern).
- **A click can be lost before it reaches the outbox** — the write happens
  after the response, and a crash in that window loses it. The alternative
  costs the redirect its latency; counting is not worth that. The requirements
  say "a click already accepted is never lost", and this is the line where
  accepted begins.

## Revisit when

Someone needs real-time click counts. The answer then is a streaming
aggregate, not a synchronous write.
