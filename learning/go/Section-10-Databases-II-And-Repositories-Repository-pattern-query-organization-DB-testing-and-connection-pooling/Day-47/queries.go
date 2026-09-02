package main

/*
Every SQL statement in this program lives in this file.

Why one file:
  - a reviewer can audit the entire data access surface in one read
  - duplicated statements become obvious instead of drifting apart
  - grep for a table name finds every place it is touched
  - queries get names, and names are what make review conversations possible

Naming convention, borrowed from sqlc and used consistently below:

	<Verb><Entity><Qualifier>SQL

	SelectAuthorByIDSQL       one row by primary key
	SelectAuthorsSQL          many rows
	SelectBooksByAuthorIDsSQL many rows, batched by a set of keys
	InsertBookSQL             a write

A name like "query1" or "getData" tells a reviewer nothing. A name that
matches the business operation tells them whether the SQL underneath is
correct without reading it twice.
*/

//
// SCHEMA
//

const SchemaSQL = `
CREATE TABLE IF NOT EXISTS authors (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	name    TEXT NOT NULL,
	country TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS books (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	author_id   INTEGER NOT NULL REFERENCES authors (id) ON DELETE CASCADE,
	title       TEXT    NOT NULL,
	year        INTEGER NOT NULL,
	price_cents INTEGER NOT NULL DEFAULT 0
);

-- Every foreign key that is used for lookups needs an index. Without it the
-- "books of this author" query degrades into a full table scan, and the N+1
-- pattern below becomes N+1 scans.
CREATE INDEX IF NOT EXISTS idx_books_author ON books (author_id, year DESC);`

//
// AUTHORS
//

const (
	InsertAuthorSQL = `
		INSERT INTO authors (name, country)
		VALUES (?, ?);`

	SelectAuthorByIDSQL = `
		SELECT id, name, country
		FROM authors
		WHERE id = ?;`

	SelectAuthorsSQL = `
		SELECT id, name, country
		FROM authors
		ORDER BY name
		LIMIT ? OFFSET ?;`
)

//
// BOOKS
//

const (
	InsertBookSQL = `
		INSERT INTO books (author_id, title, year, price_cents)
		VALUES (?, ?, ?, ?);`

	// The N+1 half: correct, but executed once per author.
	SelectBooksByAuthorIDSQL = `
		SELECT id, author_id, title, year, price_cents
		FROM books
		WHERE author_id = ?
		ORDER BY year DESC;`

	// The batched fix. The IN list is built from a placeholder count, never
	// from the values themselves, so it stays injection-safe.
	// See buildInClause in store.go.
	SelectBooksByAuthorIDsSQL = `
		SELECT id, author_id, title, year, price_cents
		FROM books
		WHERE author_id IN (%s)
		ORDER BY author_id, year DESC;`

	// The JOIN fix: one round trip, one result set, at the cost of repeating
	// the author columns on every row.
	SelectAuthorsWithBooksSQL = `
		SELECT
			a.id, a.name, a.country,
			b.id, b.title, b.year, b.price_cents
		FROM authors a
		LEFT JOIN books b ON b.author_id = a.id
		ORDER BY a.name, b.year DESC;`

	// Aggregates belong in the database, not in a Go loop over every row.
	SelectAuthorStatsSQL = `
		SELECT
			a.id,
			a.name,
			COUNT(b.id)                       AS book_count,
			COALESCE(SUM(b.price_cents), 0)   AS catalog_value_cents,
			COALESCE(MAX(b.year), 0)          AS latest_year
		FROM authors a
		LEFT JOIN books b ON b.author_id = a.id
		GROUP BY a.id, a.name
		HAVING COUNT(b.id) >= ?
		ORDER BY book_count DESC, a.name;`

	CountBooksSQL = `SELECT COUNT(*) FROM books;`
)
