# Changelog

All notable changes to the Catalog API are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

Written for the person deciding whether to upgrade — which is why it is not the
git history. "Refactor the retry loop" belongs in the history and nowhere else;
"retries now stop after 30 seconds instead of retrying forever" belongs here,
because somebody's timeout budget depends on it.

## [Unreleased]

### Added

- `internal/contract` compares `api/openapi.yaml` with the server's route
  table, so an undocumented route fails a test rather than a client integration.
- `internal/docslint` enforces package comments and godoc name prefixes.

## [1.2.0] - 2026-08-30

### Added

- `POST /products/{sku}/reservations` reserves stock. Returns `409` when the
  stock is insufficient, and changes nothing in that case.
- `stock` and `updated_at` on every product response.

### Changed

- **Breaking for error-message parsers:** error bodies now always use
  `{"error": "<code>", "message": "..."}`. Switch on `error`; `message` is for
  humans and will keep changing. Previously some endpoints returned a bare
  string.
- `GET /products` returns products ordered by SKU. It was previously map order,
  which changed between calls and broke client-side caching.

### Fixed

- `POST /products` with a duplicate SKU returned `500`; it now returns `409`.

## [1.1.0] - 2026-07-14

### Added

- `DELETE /products/{sku}`.

### Deprecated

- `GET /product?sku=` (singular, query parameter). Use `GET /products/{sku}`.
  It will be removed in 2.0.0, no earlier than 2026-12-01.

### Security

- Request bodies are capped at 1 MiB. Previously a large body could exhaust
  memory before the handler ever ran.

## [1.0.0] - 2026-06-02

### Added

- `GET /products`, `POST /products`, `GET /products/{sku}`, `GET /healthz`.
- `api/openapi.yaml`, the contract clients build against.
