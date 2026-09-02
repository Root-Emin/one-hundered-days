// Package notes is the small service this day's tooling operates on.
//
// It is deliberately unremarkable: the subject of Day 93 is the Makefile, the
// setup script and the hooks, and those need something real to build, test,
// migrate and run - not something interesting enough to distract from them.
package notes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound means no note has that id.
var ErrNotFound = errors.New("note not found")

// Note is one stored note.
type Note struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Store persists notes in a SQL database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create stores a note and returns it with its id and timestamp.
func (s *Store) Create(ctx context.Context, title, body string) (Note, error) {
	if title == "" {
		return Note{}, errors.New("title is required")
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (title, body) VALUES (?, ?);`, title, body)
	if err != nil {
		return Note{}, fmt.Errorf("insert note: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Note{}, fmt.Errorf("read note id: %w", err)
	}

	return s.Get(ctx, id)
}

// Get returns one note, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Note, error) {
	var (
		note      Note
		createdAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, body, created_at FROM notes WHERE id = ?;`, id).
		Scan(&note.ID, &note.Title, &note.Body, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, fmt.Errorf("note %d: %w", id, ErrNotFound)
	}

	if err != nil {
		return Note{}, fmt.Errorf("select note %d: %w", id, err)
	}

	if note.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
		return Note{}, fmt.Errorf("parse created_at: %w", err)
	}

	return note, nil
}

// List returns every note, newest first.
func (s *Store) List(ctx context.Context) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, body, created_at FROM notes ORDER BY id DESC;`)
	if err != nil {
		return nil, fmt.Errorf("select notes: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var notes []Note

	for rows.Next() {
		var (
			note      Note
			createdAt string
		)

		if err := rows.Scan(&note.ID, &note.Title, &note.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}

		if note.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notes: %w", err)
	}

	return notes, nil
}

// Count returns how many notes are stored.
func (s *Store) Count(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count notes: %w", err)
	}

	return count, nil
}
