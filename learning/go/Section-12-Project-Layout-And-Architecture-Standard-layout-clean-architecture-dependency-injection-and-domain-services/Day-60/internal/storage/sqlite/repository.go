// Package sqlite implements the domain's storage ports on top of database/sql.
//
// It is the only package in the service that contains SQL, and the only one
// that imports a driver. Everything it knows about the business is what the
// domain types tell it.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/domain"
)

const schema = `
CREATE TABLE IF NOT EXISTS books (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	isbn        TEXT    NOT NULL,
	title       TEXT    NOT NULL,
	author      TEXT    NOT NULL,
	pages       INTEGER NOT NULL CHECK (pages > 0),
	status      TEXT    NOT NULL DEFAULT 'wishlist',
	progress    INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0),
	added_at    TEXT    NOT NULL,
	finished_at TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_books_isbn ON books (isbn);
CREATE INDEX IF NOT EXISTS idx_books_status ON books (status, added_at DESC);`

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Both ports are satisfied by the same type here; they stay separate
// interfaces so a caller can depend on only the half it needs.
var (
	_ domain.BookRepository = (*Repository)(nil)
	_ domain.StatsReader    = (*Repository)(nil)
)

// Open returns a ready pool: connected, migrated, and tuned.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

const columns = `id, isbn, title, author, pages, status, progress, added_at, finished_at`

func (r *Repository) Create(ctx context.Context, book domain.Book) (domain.Book, error) {
	const query = `
		INSERT INTO books (isbn, title, author, pages, status, progress, added_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?);`

	result, err := r.db.ExecContext(ctx, query,
		book.ISBN.String(), book.Title, book.Author, book.Pages,
		string(book.Status), book.Progress,
		book.AddedAt.Format(time.RFC3339), formatOptional(book.FinishedAt))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return domain.Book{}, fmt.Errorf("create book %s: %w", book.ISBN, domain.ErrConflict)
		}

		return domain.Book{}, fmt.Errorf("create book %s: %w", book.ISBN, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return domain.Book{}, fmt.Errorf("create book: read id: %w", err)
	}

	return r.ByID(ctx, id)
}

func (r *Repository) Update(ctx context.Context, book domain.Book) (domain.Book, error) {
	const query = `
		UPDATE books
		SET status = ?, progress = ?, finished_at = ?
		WHERE id = ?;`

	result, err := r.db.ExecContext(ctx, query,
		string(book.Status), book.Progress, formatOptional(book.FinishedAt), book.ID)
	if err != nil {
		return domain.Book{}, fmt.Errorf("update book %d: %w", book.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Book{}, fmt.Errorf("update book %d: rows affected: %w", book.ID, err)
	}

	if affected == 0 {
		return domain.Book{}, fmt.Errorf("book %d: %w", book.ID, domain.ErrNotFound)
	}

	return r.ByID(ctx, book.ID)
}

func (r *Repository) ByID(ctx context.Context, id int64) (domain.Book, error) {
	book, err := scanBook(r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM books WHERE id = ?;`, id))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Book{}, fmt.Errorf("book %d: %w", id, domain.ErrNotFound)
	case err != nil:
		return domain.Book{}, fmt.Errorf("select book %d: %w", id, err)
	}

	return book, nil
}

func (r *Repository) ByISBN(ctx context.Context, isbn domain.ISBN) (domain.Book, error) {
	book, err := scanBook(r.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM books WHERE isbn = ?;`, isbn.String()))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.Book{}, fmt.Errorf("book %s: %w", isbn, domain.ErrNotFound)
	case err != nil:
		return domain.Book{}, fmt.Errorf("select book %s: %w", isbn, err)
	}

	return book, nil
}

func (r *Repository) List(ctx context.Context, status domain.Status, limit, offset int) ([]domain.Book, error) {
	query := `SELECT ` + columns + `
		FROM books
		WHERE (? = '' OR status = ?)
		ORDER BY added_at DESC, id DESC
		LIMIT ? OFFSET ?;`

	rows, err := r.db.QueryContext(ctx, query, string(status), string(status), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}

	defer closeRows(rows)

	books := make([]domain.Book, 0, limit)

	for rows.Next() {
		book, err := scanBook(rows)
		if err != nil {
			return nil, fmt.Errorf("list books: %w", err)
		}

		books = append(books, book)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}

	return books, nil
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete book %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete book %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		return fmt.Errorf("book %d: %w", id, domain.ErrNotFound)
	}

	return nil
}

// Stats is the read model: one aggregate query instead of loading every row
// into Go to count them.
func (r *Repository) Stats(ctx context.Context) (domain.Stats, error) {
	const query = `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'reading'  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'finished' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(progress), 0),
			COALESCE(SUM(pages), 0)
		FROM books;`

	var stats domain.Stats

	if err := r.db.QueryRowContext(ctx, query).Scan(
		&stats.Total, &stats.Reading, &stats.Finished, &stats.PagesRead, &stats.PagesTotal,
	); err != nil {
		return domain.Stats{}, fmt.Errorf("read stats: %w", err)
	}

	return stats, nil
}

//
// MAPPING
//

type rowScanner interface {
	Scan(dest ...any) error
}

// scanBook is the only place that knows the column order, and the only place
// that turns stored strings back into domain types.
func scanBook(row rowScanner) (domain.Book, error) {
	var (
		book       domain.Book
		rawISBN    string
		status     string
		addedAt    string
		finishedAt string
	)

	if err := row.Scan(&book.ID, &rawISBN, &book.Title, &book.Author, &book.Pages,
		&status, &book.Progress, &addedAt, &finishedAt); err != nil {
		return domain.Book{}, err
	}

	isbn, err := domain.NewISBN(rawISBN)
	if err != nil {
		// A row that cannot become a domain value is corrupt data, and saying
		// so beats silently returning a half-built entity.
		return domain.Book{}, fmt.Errorf("stored isbn %q is invalid: %w", rawISBN, err)
	}

	book.ISBN = isbn
	book.Status = domain.Status(status)

	if book.AddedAt, err = time.Parse(time.RFC3339, addedAt); err != nil {
		return domain.Book{}, fmt.Errorf("parse added_at %q: %w", addedAt, err)
	}

	if finishedAt != "" {
		if book.FinishedAt, err = time.Parse(time.RFC3339, finishedAt); err != nil {
			return domain.Book{}, fmt.Errorf("parse finished_at %q: %w", finishedAt, err)
		}
	}

	return book, nil
}

func formatOptional(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.Format(time.RFC3339)
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("close rows: %v", err)
	}
}
