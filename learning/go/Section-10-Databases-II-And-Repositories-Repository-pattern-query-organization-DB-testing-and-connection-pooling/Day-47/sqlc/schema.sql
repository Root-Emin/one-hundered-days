-- Schema copy for sqlc. The runtime source of truth is SchemaSQL in
-- ../queries.go; sqlc needs the DDL as a real .sql file to infer column types.

CREATE TABLE authors (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	country TEXT NOT NULL DEFAULT ''
);

CREATE TABLE books (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	author_id   INTEGER NOT NULL REFERENCES authors (id) ON DELETE CASCADE,
	title       TEXT    NOT NULL,
	year        INTEGER NOT NULL,
	price_cents INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_books_author ON books (author_id, year DESC);
