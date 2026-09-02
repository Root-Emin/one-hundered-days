// Package store is Linkr's persistence: SQLite, migrations, and the outbox.
//
// It implements the ports the service package defines, and it is the only
// package in the project that knows SQL exists.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-97/internal/domain"
)

// Store is the database.
type Store struct {
	db *sql.DB
}

// Open connects to SQLite and returns a Store.
//
// The pragmas are not decoration:
//
//	journal_mode=WAL   a reader (the redirect) and the writer (the worker) stop
//	                   blocking each other, which is the difference between a
//	                   redirect that waits and one that does not
//	busy_timeout       a concurrent writer waits instead of failing instantly
//	foreign_keys       SQLite does NOT enforce them by default, which surprises
//	                   everyone exactly once
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	// SQLite serialises writes anyway, and an unbounded pool against one file
	// buys nothing but file descriptors.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}

	return &Store{db: db}, nil
}

// DB exposes the handle for migrations and tests.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close releases the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}

	return nil
}

// Check implements the readiness probe's dependency check.
func (s *Store) Check(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}

	return nil
}

//
// LINKS
//

// CreateLink stores a new link.
//
// A duplicate code returns domain.ErrCodeTaken so the caller can retry with a
// fresh one; every other failure is wrapped and returned as-is.
func (s *Store) CreateLink(ctx context.Context, link domain.Link) error {
	var expires any

	if !link.ExpiresAt.IsZero() {
		expires = link.ExpiresAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO links (code, owner, target, active, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?);`,
		link.Code.String(), link.Owner, link.Target, link.Active, expires,
		link.CreatedAt.UTC().Format(time.RFC3339))

	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s: %w", link.Code, domain.ErrCodeTaken)
		}

		return fmt.Errorf("insert link: %w", err)
	}

	return nil
}

// Link returns one link by code, or domain.ErrNotFound.
//
// This is the hot path's only query. It is a primary-key lookup on purpose.
func (s *Store) Link(ctx context.Context, code domain.Code) (domain.Link, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT code, owner, target, active, expires_at, created_at FROM links WHERE code = ?;`,
		code.String())

	link, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Link{}, fmt.Errorf("%s: %w", code, domain.ErrNotFound)
	}

	if err != nil {
		return domain.Link{}, fmt.Errorf("select link %s: %w", code, err)
	}

	return link, nil
}

// LinksByOwner returns an owner's links, newest first.
func (s *Store) LinksByOwner(ctx context.Context, owner string, limit int) ([]domain.Link, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT code, owner, target, active, expires_at, created_at
		 FROM links WHERE owner = ? ORDER BY created_at DESC, code LIMIT ?;`, owner, limit)
	if err != nil {
		return nil, fmt.Errorf("select links: %w", err)
	}

	defer closeRows(rows)

	var links []domain.Link

	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate links: %w", err)
	}

	return links, nil
}

// DeactivateLink turns a link off.
//
// The row stays, and the code is never reused: a deleted row would let a new
// link inherit an old one's traffic, which is a redirect nobody audited.
func (s *Store) DeactivateLink(ctx context.Context, owner string, code domain.Code) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE links SET active = 0 WHERE code = ? AND owner = ?;`, code.String(), owner)
	if err != nil {
		return fmt.Errorf("deactivate link: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if affected == 0 {
		// Not found and not-yours are the same answer on purpose: telling a
		// caller that a code exists but belongs to someone else is an
		// enumeration oracle.
		return fmt.Errorf("%s: %w", code, domain.ErrNotFound)
	}

	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves the single-row and multi-row paths.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanLink(scanner rowScanner) (domain.Link, error) {
	var (
		link      domain.Link
		code      string
		expires   sql.NullString
		createdAt string
	)

	if err := scanner.Scan(&code, &link.Owner, &link.Target, &link.Active, &expires, &createdAt); err != nil {
		return domain.Link{}, err
	}

	link.Code = domain.Code(code)

	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return domain.Link{}, fmt.Errorf("parse created_at: %w", err)
	}

	link.CreatedAt = parsed

	if expires.Valid && expires.String != "" {
		if link.ExpiresAt, err = time.Parse(time.RFC3339, expires.String); err != nil {
			return domain.Link{}, fmt.Errorf("parse expires_at: %w", err)
		}
	}

	return link, nil
}

//
// API KEYS
//

// APIKey is a stored credential. The plaintext key is never persisted.
type APIKey struct {
	ID        string
	Owner     string
	Hash      string
	CreatedAt time.Time
}

// CreateAPIKey stores a hashed key.
func (s *Store) CreateAPIKey(ctx context.Context, key APIKey) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, owner, hash, created_at) VALUES (?, ?, ?, ?);`,
		key.ID, key.Owner, key.Hash, key.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}

	return nil
}

// OwnerForHash returns the owner a key hash belongs to.
//
// The lookup is by hash, which is indexed: the plaintext key never reaches the
// database, so a query log or a slow-query trace cannot leak one.
func (s *Store) OwnerForHash(ctx context.Context, hash string) (string, error) {
	var owner string

	err := s.db.QueryRowContext(ctx, `SELECT owner FROM api_keys WHERE hash = ?;`, hash).Scan(&owner)

	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrUnauthorized
	}

	if err != nil {
		return "", fmt.Errorf("select api key: %w", err)
	}

	return owner, nil
}

// TouchAPIKey records that a key was used.
//
// Best effort and deliberately not in the request's critical path: turning
// every read into a write is how a rate-limited API becomes a write-limited
// one.
func (s *Store) TouchAPIKey(ctx context.Context, hash string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE hash = ?;`,
		time.Now().UTC().Format(time.RFC3339), hash); err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}

	return nil
}

//
// CLICKS AND THE OUTBOX
//

// RecordClick writes a click and its outbox event in ONE transaction.
//
// Both or neither: an event with no click is a phantom in the aggregate, and a
// click with no event never reaches click_daily.
func (s *Store) RecordClick(ctx context.Context, click domain.Click) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	committed := false

	defer func() {
		if committed {
			return
		}

		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = err
		}
	}()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO clicks (code, occurred_at, referrer, user_agent) VALUES (?, ?, ?, ?);`,
		click.Code.String(), click.OccurredAt.UTC().Format(time.RFC3339), click.Referrer, click.UserAgent)
	if err != nil {
		return fmt.Errorf("insert click: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read click id: %w", err)
	}

	payload, err := json.Marshal(map[string]any{
		"click_id": id,
		"code":     click.Code.String(),
		"day":      click.Day(),
	})
	if err != nil {
		return fmt.Errorf("encode click event: %w", err)
	}

	// The event id is derived from the click row, so a retry of this whole
	// operation cannot produce two events for one click.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_id, event_type, payload, created_at) VALUES (?, ?, ?, ?);`,
		fmt.Sprintf("click-%d", id), "click.recorded", string(payload),
		click.OccurredAt.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert outbox row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit click: %w", err)
	}

	committed = true

	return nil
}

// ClickCount returns the total clicks recorded for a code.
func (s *Store) ClickCount(ctx context.Context, code domain.Code) (int64, error) {
	var count int64

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM clicks WHERE code = ?;`, code.String()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count clicks: %w", err)
	}

	return count, nil
}

// PendingEvents returns how many outbox rows are waiting, which is the number
// an alert watches.
func (s *Store) PendingEvents(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE published_at IS NULL;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending events: %w", err)
	}

	return count, nil
}

func isUniqueViolation(err error) bool {
	message := strings.ToUpper(err.Error())

	return strings.Contains(message, "UNIQUE CONSTRAINT FAILED") ||
		strings.Contains(message, "SQLSTATE 23505")
}
