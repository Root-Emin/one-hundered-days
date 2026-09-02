# ADR 0001 — SQLite, not PostgreSQL

**Status:** accepted · **Date:** 2026-09-02

## Context

The service needs durable storage for links, clicks and the outbox. The
alternatives are PostgreSQL (what this would be in production at scale) and
SQLite (embedded, one file).

## Decision

SQLite, in WAL mode, with the schema and access layer written so that moving to
PostgreSQL is a driver change and a handful of query edits — not a rewrite.

## Why

- **The workload fits.** A link shortener is read-heavy, and its reads are
  point lookups by primary key. SQLite does hundreds of thousands of those a
  second on a laptop.
- **Operational cost is the whole argument.** One file, no server, no
  connection pool to size, no backup daemon. For a service with one instance,
  the database being a process is a liability rather than a feature.
- **It keeps the tests honest.** Every test runs against the real engine
  instead of a mock, in milliseconds, with no container.

## Consequences

- **One writer.** SQLite serialises writes. Fine here — the write rate is link
  creation plus batched click aggregation, not the redirect path.
- **No horizontal scaling.** A second instance cannot share the file. This is
  the first thing that breaks if the service grows, and it is written down in
  the architecture's "deliberately absent" table.
- The code avoids SQLite-only syntax where a portable form exists, so the
  migration is mechanical.

## Revisit when

The write rate exceeds what one process can serialise, or a second instance is
needed for availability rather than throughput.
