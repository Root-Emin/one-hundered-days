# Changelog

Notable changes to the task tracker, for the person deciding whether to
upgrade. Follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

## [0.2.0] - 2026-09-02

### Added

- `POST /tasks/{id}/advance` moves a task forward. Returns `409` when the move
  is backwards or to the same state, and changes nothing in that case.
- `GET /tasks?status=` filters by status.
- `internal/repoaudit` and `internal/selfreview`, which check this repository's
  own documentation and code against the review checklist.

### Changed

- **Breaking for anyone parsing error strings:** every non-2xx response now
  uses `{"error": "<code>", "message": "..."}`. Switch on `error`, which is a
  stable code; `message` is for humans and will keep changing.
- `GET /tasks` returns tasks ordered by id. It was previously map order, which
  changed between calls.

### Fixed

- Advancing a task twice in a row returned `200` and silently did nothing; it
  now returns `409`, because a repeated advance is almost always a double
  submit.

## [0.1.0] - 2026-08-20

### Added

- `GET /tasks`, `POST /tasks`, `GET /healthz`.
- The three-state task model with forward-only transitions.
