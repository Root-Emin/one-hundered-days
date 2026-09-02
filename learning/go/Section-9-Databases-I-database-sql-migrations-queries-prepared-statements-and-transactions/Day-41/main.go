package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	// The driver registers itself with database/sql under the name "sqlite".
	// Our code below never imports it directly again: it talks to the
	// database through the database/sql interface only. Swapping SQLite for
	// Postgres later means changing this import and the DSN, nothing else.
	_ "modernc.org/sqlite"
)

/*
Day 41 - Databases (I): database/sql Introduction

Tasks covered:

 1. Open a DB connection with the right driver import
 2. Ping the database on startup and fail fast if unreachable
 3. Query rows with QueryContext and scan into variables
 4. Defer Close on rows and the pool to avoid leaks

Run:

	go run main.go

Environment variables:

	DB_PATH  SQLite file path. Default: ./data/day41.db
	         Use ":memory:" for a throwaway in-memory database.

Example:

	DB_PATH=:memory: go run main.go

Why SQLite here: modernc.org/sqlite is a pure Go driver, so this day runs
with no database server to install. Everything below is plain database/sql,
so the same code shape applies to Postgres or MySQL.
*/

//
// CONFIG
//

const (
	defaultDBPath = "data/day41.db"

	// Startup should not hang forever on an unreachable database.
	pingTimeout = 5 * time.Second

	// Every query in a server gets a deadline. No exceptions.
	queryTimeout = 3 * time.Second
)

//
// MODEL
//

type Book struct {
	ID        int64
	Title     string
	Author    string
	Year      int
	CreatedAt time.Time
}

func (b Book) String() string {
	return fmt.Sprintf("#%d %-28s %-20s %d", b.ID, b.Title, b.Author, b.Year)
}

//
// CONNECTION
//

// openDB opens the pool and verifies it is actually usable.
//
// sql.Open does NOT connect: it only validates arguments and prepares a lazy
// pool. Without the Ping below, the first real failure would surface inside a
// request handler instead of at startup.
func openDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Pool limits. Day 49 goes deeper; these are sane starting values.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		// Close the pool we just opened, otherwise the failed startup leaks it.
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database after failed ping: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}

// resolveDSN turns the configured path into a driver DSN, creating the parent
// directory when the database lives on disk.
func resolveDSN() (string, error) {
	path := os.Getenv("DB_PATH")

	if path == "" {
		path = defaultDBPath
	}

	if path == ":memory:" {
		return path, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create database directory: %w", err)
	}

	return path, nil
}

//
// SCHEMA + SEED
//

const schema = `
CREATE TABLE IF NOT EXISTS books (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT    NOT NULL,
	author     TEXT    NOT NULL,
	year       INTEGER NOT NULL,
	created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);`

func migrate(ctx context.Context, db *sql.DB) error {
	// ExecContext is for statements that return no rows.
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create books table: %w", err)
	}

	return nil
}

func seed(ctx context.Context, db *sql.DB) error {
	count, err := countBooks(ctx, db)
	if err != nil {
		return err
	}

	if count > 0 {
		log.Printf("seed skipped, %d books already stored", count)
		return nil
	}

	books := []Book{
		{Title: "The Go Programming Language", Author: "Donovan & Kernighan", Year: 2015},
		{Title: "Go in Action", Author: "Kennedy", Year: 2015},
		{Title: "Concurrency in Go", Author: "Cox-Buday", Year: 2017},
		{Title: "Learning Go", Author: "Bodner", Year: 2021},
		{Title: "100 Go Mistakes", Author: "Harsanyi", Year: 2022},
	}

	// Placeholders (?) keep values as data. Never build SQL with fmt.Sprintf
	// and user input - that is how SQL injection happens (Day 43).
	const insert = `INSERT INTO books (title, author, year) VALUES (?, ?, ?);`

	for _, book := range books {
		if _, err := db.ExecContext(ctx, insert, book.Title, book.Author, book.Year); err != nil {
			return fmt.Errorf("seed book %q: %w", book.Title, err)
		}
	}

	log.Printf("seeded %d books", len(books))

	return nil
}

//
// QUERIES
//

// listBooks demonstrates the full multi-row read cycle:
// QueryContext -> defer rows.Close -> rows.Next/Scan -> rows.Err.
func listBooks(ctx context.Context, db *sql.DB, minYear int) ([]Book, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	const query = `
		SELECT id, title, author, year, created_at
		FROM books
		WHERE year >= ?
		ORDER BY year DESC, title ASC;`

	rows, err := db.QueryContext(ctx, query, minYear)
	if err != nil {
		return nil, fmt.Errorf("select books: %w", err)
	}

	// Rows hold a connection from the pool until they are closed. Forgetting
	// this defer is the classic way to exhaust a pool in production.
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("close rows: %v", err)
		}
	}()

	var books []Book

	for rows.Next() {
		var (
			book      Book
			createdAt string
		)

		// Scan copies column values into Go variables. The order and count
		// must match the SELECT list exactly.
		if err := rows.Scan(&book.ID, &book.Title, &book.Author, &book.Year, &createdAt); err != nil {
			return nil, fmt.Errorf("scan book row: %w", err)
		}

		book.CreatedAt, err = time.Parse(time.DateTime, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", createdAt, err)
		}

		books = append(books, book)
	}

	// rows.Next returns false both at the end of the result set and on error.
	// Only rows.Err can tell the difference, so it is never optional.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate book rows: %w", err)
	}

	return books, nil
}

// findBookByID shows the single-row path. QueryRowContext defers its error to
// Scan, and reports "nothing found" as sql.ErrNoRows.
func findBookByID(ctx context.Context, db *sql.DB, id int64) (Book, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	const query = `
		SELECT id, title, author, year, created_at
		FROM books
		WHERE id = ?;`

	var (
		book      Book
		createdAt string
	)

	err := db.QueryRowContext(ctx, query, id).
		Scan(&book.ID, &book.Title, &book.Author, &book.Year, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// An empty result is not a failure of the query; it is an answer.
		// Day 43 turns this into a proper domain error.
		return Book{}, fmt.Errorf("book %d: %w", id, err)

	case err != nil:
		return Book{}, fmt.Errorf("select book %d: %w", id, err)
	}

	book.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Book{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return book, nil
}

func countBooks(ctx context.Context, db *sql.DB) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var count int

	// COUNT(*) always returns exactly one row, so ErrNoRows is impossible here.
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count books: %w", err)
	}

	return count, nil
}

//
// MAIN
//

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day41: ")

	ctx := context.Background()

	dsn, err := resolveDSN()
	if err != nil {
		log.Fatalf("resolve dsn: %v", err)
	}

	db, err := openDB(ctx, dsn)
	if err != nil {
		// Fail fast: a service that cannot reach its database must not start.
		log.Fatalf("database unavailable: %v", err)
	}

	// The pool is a long-lived resource. Close it once, on the way out.
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	log.Printf("connected dsn=%s driver=sqlite", dsn)

	if err := migrate(ctx, db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	if err := seed(ctx, db); err != nil {
		log.Fatalf("seed: %v", err)
	}

	//
	// Multi-row query
	//

	const minYear = 2015

	books, err := listBooks(ctx, db, minYear)
	if err != nil {
		log.Fatalf("list books: %v", err)
	}

	fmt.Printf("\nBooks published in %d or later (%d found)\n", minYear, len(books))
	fmt.Println("--------------------------------------------------------------")

	for _, book := range books {
		fmt.Println(book)
	}

	//
	// Single-row query: found
	//

	fmt.Println()

	book, err := findBookByID(ctx, db, 1)
	if err != nil {
		log.Fatalf("find book: %v", err)
	}

	fmt.Printf("Found book 1: %s (stored %s)\n", book, book.CreatedAt.Format(time.DateTime))

	//
	// Single-row query: not found
	//

	if _, err := findBookByID(ctx, db, 9999); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fmt.Println("Book 9999: not found (sql.ErrNoRows handled, not a crash)")
		} else {
			log.Fatalf("find book: %v", err)
		}
	}

	//
	// Pool stats: proof that connections were returned, not leaked
	//

	stats := db.Stats()

	fmt.Printf(
		"\nPool: open=%d in_use=%d idle=%d wait_count=%d\n",
		stats.OpenConnections,
		stats.InUse,
		stats.Idle,
		stats.WaitCount,
	)

	log.Printf("done")
}
