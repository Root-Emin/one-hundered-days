-- Optional exploration: the same queries in sqlc's annotated format.
--
-- sqlc reads this file plus the schema, and generates type-safe Go: one
-- struct per row shape and one method per query, with the column types taken
-- from the schema instead of from a Scan call you wrote by hand.
--
-- Install and run (not required for today's tasks):
--
--     go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
--     sqlc generate
--
-- What you gain: a renamed or retyped column becomes a compile error instead
-- of a runtime Scan failure.
-- What you give up: dynamic SQL (the IN-clause builder in store.go) needs
-- hand-written code anyway, and the generated code is another artifact to
-- keep in review.

-- name: GetAuthorByID :one
SELECT id, name, country
FROM authors
WHERE id = ?;

-- name: ListAuthors :many
SELECT id, name, country
FROM authors
ORDER BY name
LIMIT ? OFFSET ?;

-- name: ListBooksByAuthorID :many
SELECT id, author_id, title, year, price_cents
FROM books
WHERE author_id = ?
ORDER BY year DESC;

-- name: CreateBook :one
INSERT INTO books (author_id, title, year, price_cents)
VALUES (?, ?, ?, ?)
RETURNING id, author_id, title, year, price_cents;
