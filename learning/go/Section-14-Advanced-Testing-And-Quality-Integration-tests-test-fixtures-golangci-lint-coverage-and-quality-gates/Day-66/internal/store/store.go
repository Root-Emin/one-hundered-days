// Package store is the persistence layer for the bookmarks API.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("bookmark not found")
	ErrDuplicate = errors.New("url already saved")
)

const Schema = `
CREATE TABLE IF NOT EXISTS bookmarks (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	owner      TEXT NOT NULL,
	url        TEXT NOT NULL,
	title      TEXT NOT NULL,
	tags       TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_bookmarks_owner_url ON bookmarks (owner, url);
CREATE INDEX IF NOT EXISTS idx_bookmarks_owner ON bookmarks (owner, created_at DESC);`

type Bookmark struct {
	ID        int64
	Owner     string
	URL       string
	Title     string
	Tags      []string
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.ExecContext(ctx, Schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

func (s *Store) Create(ctx context.Context, bookmark Bookmark) (Bookmark, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO bookmarks (owner, url, title, tags) VALUES (?, ?, ?, ?);`,
		bookmark.Owner, bookmark.URL, bookmark.Title, strings.Join(bookmark.Tags, ","))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return Bookmark{}, fmt.Errorf("create bookmark: %w", ErrDuplicate)
		}

		return Bookmark{}, fmt.Errorf("create bookmark: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Bookmark{}, fmt.Errorf("create bookmark: read id: %w", err)
	}

	return s.ByID(ctx, id)
}

func (s *Store) ByID(ctx context.Context, id int64) (Bookmark, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner, url, title, tags, created_at FROM bookmarks WHERE id = ?;`, id)

	bookmark, err := scan(row)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Bookmark{}, fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	case err != nil:
		return Bookmark{}, fmt.Errorf("select bookmark %d: %w", id, err)
	}

	return bookmark, nil
}

func (s *Store) ListByOwner(ctx context.Context, owner, tag string, limit int) ([]Bookmark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner, url, title, tags, created_at
		 FROM bookmarks
		 WHERE owner = ? AND (? = '' OR tags LIKE '%' || ? || '%')
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`, owner, tag, tag, limit)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("close rows: %v", err)
		}
	}()

	bookmarks := make([]Bookmark, 0, limit)

	for rows.Next() {
		bookmark, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list bookmarks: %w", err)
		}

		bookmarks = append(bookmarks, bookmark)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}

	return bookmarks, nil
}

func (s *Store) Delete(ctx context.Context, owner string, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM bookmarks WHERE id = ? AND owner = ?;`, id, owner)
	if err != nil {
		return fmt.Errorf("delete bookmark %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete bookmark %d: rows affected: %w", id, err)
	}

	if affected == 0 {
		return fmt.Errorf("bookmark %d: %w", id, ErrNotFound)
	}

	return nil
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count bookmarks: %w", err)
	}

	return count, nil
}

// Truncate is used by test helpers between cases.
func (s *Store) Truncate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM bookmarks;`); err != nil {
		return fmt.Errorf("truncate bookmarks: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name = 'bookmarks';`); err != nil {
		return fmt.Errorf("reset sequence: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scan(row rowScanner) (Bookmark, error) {
	var (
		bookmark  Bookmark
		tags      string
		createdAt string
	)

	if err := row.Scan(&bookmark.ID, &bookmark.Owner, &bookmark.URL,
		&bookmark.Title, &tags, &createdAt); err != nil {
		return Bookmark{}, err
	}

	if tags != "" {
		bookmark.Tags = strings.Split(tags, ",")
	}

	parsed, err := time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Bookmark{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	bookmark.CreatedAt = parsed

	return bookmark, nil
}
