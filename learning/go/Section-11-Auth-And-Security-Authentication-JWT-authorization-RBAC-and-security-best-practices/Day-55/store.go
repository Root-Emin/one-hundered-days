package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/*
Persistence for the secured MVP: users, their roles, refresh tokens and an
audit trail.

Storage rules carried over from the section:
  - passwords are bcrypt hashes
  - refresh tokens are stored as SHA-256 hashes, never in the clear
  - every security-relevant action gets an audit row
*/

var (
	ErrNotFound     = errors.New("not found")
	ErrEmailTaken   = errors.New("email already registered")
	ErrTokenUnknown = errors.New("refresh token is not valid")
)

const Schema = `
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	email         TEXT NOT NULL,
	display_name  TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL DEFAULT 'member',
	suspended     INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (email);

CREATE TABLE IF NOT EXISTS notes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	owner_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	title      TEXT NOT NULL,
	body       TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_notes_owner ON notes (owner_id, created_at DESC);

CREATE TABLE IF NOT EXISTS refresh_tokens (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL,
	used       INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens (user_id);

CREATE TABLE IF NOT EXISTS audit_log (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	actor_id   INTEGER,
	action     TEXT NOT NULL,
	detail     TEXT NOT NULL DEFAULT '',
	ip         TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

type User struct {
	ID           int64
	Email        string
	DisplayName  string
	Role         Role
	Suspended    bool
	CreatedAt    time.Time
	PasswordHash string // never serialized; see api.go
}

type Note struct {
	ID        int64     `json:"id"`
	OwnerID   int64     `json:"owner_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditEntry struct {
	ID      int64  `json:"id"`
	ActorID *int64 `json:"actor_id"`
	Action  string `json:"action"`
	Detail  string `json:"detail"`
	IP      string `json:"ip"`
	At      string `json:"at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

//
// USERS
//

func (s *Store) CreateUser(ctx context.Context, email, displayName, passwordHash string, role Role) (User, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES (?, ?, ?, ?);`,
		email, displayName, passwordHash, string(role))
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return User{}, fmt.Errorf("create user: %w", ErrEmailTaken)
		}

		return User{}, fmt.Errorf("create user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("create user: read id: %w", err)
	}

	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, role, suspended, created_at
		 FROM users WHERE id = ?;`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, role, suspended, created_at
		 FROM users WHERE email = ?;`, strings.ToLower(strings.TrimSpace(email))))
}

func (s *Store) scanUser(row *sql.Row) (User, error) {
	var (
		user      User
		role      string
		createdAt string
	)

	err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash,
		&role, &user.Suspended, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return User{}, ErrNotFound
	case err != nil:
		return User{}, fmt.Errorf("scan user: %w", err)
	}

	user.Role = Role(role)

	user.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return User{}, fmt.Errorf("parse created_at: %w", err)
	}

	return user, nil
}

func (s *Store) SetSuspended(ctx context.Context, userID int64, suspended bool) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE users SET suspended = ? WHERE id = ?;`, suspended, userID)
	if err != nil {
		return fmt.Errorf("suspend user %d: %w", userID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("suspend user %d: %w", userID, err)
	}

	if affected == 0 {
		return ErrNotFound
	}

	// A suspended user must not be able to keep working with tokens issued
	// before the suspension: drop every refresh token they hold.
	if suspended {
		return s.RevokeUserTokens(ctx, userID)
	}

	return nil
}

func (s *Store) ListUsers(ctx context.Context, limit int) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, display_name, password_hash, role, suspended, created_at
		 FROM users ORDER BY id LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	defer closeRows(rows)

	var users []User

	for rows.Next() {
		var (
			user      User
			role      string
			createdAt string
		)

		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash,
			&role, &user.Suspended, &createdAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}

		user.Role = Role(role)

		if user.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	return users, nil
}

//
// NOTES
//

func (s *Store) CreateNote(ctx context.Context, ownerID int64, title, body string) (Note, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (owner_id, title, body) VALUES (?, ?, ?);`, ownerID, title, body)
	if err != nil {
		return Note{}, fmt.Errorf("create note: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Note{}, fmt.Errorf("create note: read id: %w", err)
	}

	return s.NoteByID(ctx, id)
}

func (s *Store) NoteByID(ctx context.Context, id int64) (Note, error) {
	var (
		note      Note
		createdAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, owner_id, title, body, created_at FROM notes WHERE id = ?;`, id).
		Scan(&note.ID, &note.OwnerID, &note.Title, &note.Body, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Note{}, ErrNotFound
	case err != nil:
		return Note{}, fmt.Errorf("select note %d: %w", id, err)
	}

	note.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Note{}, fmt.Errorf("parse created_at: %w", err)
	}

	return note, nil
}

func (s *Store) ListNotes(ctx context.Context, ownerID int64, limit int) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_id, title, body, created_at
		 FROM notes WHERE owner_id = ? ORDER BY created_at DESC, id DESC LIMIT ?;`,
		ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}

	defer closeRows(rows)

	notes := make([]Note, 0, limit)

	for rows.Next() {
		var (
			note      Note
			createdAt string
		)

		if err := rows.Scan(&note.ID, &note.OwnerID, &note.Title, &note.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("scan note: %w", err)
		}

		if note.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		notes = append(notes, note)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}

	return notes, nil
}

func (s *Store) DeleteNote(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ?;`, id)
	if err != nil {
		return fmt.Errorf("delete note %d: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete note %d: %w", id, err)
	}

	if affected == 0 {
		return ErrNotFound
	}

	return nil
}

//
// REFRESH TOKENS
//

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Store) StoreRefreshToken(ctx context.Context, token string, userID int64, ttl time.Duration) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (token_hash, user_id, expires_at) VALUES (?, ?, ?);`,
		HashToken(token), userID, time.Now().UTC().Add(ttl).Format(time.DateTime)); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}

	return nil
}

// RedeemRefreshToken consumes a token exactly once. A second use means the
// token leaked, so every token of that user is dropped and the caller is told
// to force a full re-authentication.
func (s *Store) RedeemRefreshToken(ctx context.Context, token string) (int64, error) {
	hash := HashToken(token)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("redeem refresh token: begin: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("redeem refresh token: rollback: %v", err)
		}
	}()

	var (
		userID    int64
		used      bool
		expiresAt string
	)

	err = tx.QueryRowContext(ctx,
		`SELECT user_id, used, expires_at FROM refresh_tokens WHERE token_hash = ?;`, hash).
		Scan(&userID, &used, &expiresAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, ErrTokenUnknown
	case err != nil:
		return 0, fmt.Errorf("redeem refresh token: %w", err)
	}

	if used {
		if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?;`, userID); err != nil {
			return 0, fmt.Errorf("revoke token family: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("revoke token family: commit: %w", err)
		}

		return userID, fmt.Errorf("%w: replayed, all sessions revoked", ErrTokenUnknown)
	}

	expiry, err := time.Parse(time.DateTime, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("parse expires_at: %w", err)
	}

	if time.Now().UTC().After(expiry) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE token_hash = ?;`, hash); err != nil {
			return 0, fmt.Errorf("delete expired token: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("delete expired token: commit: %w", err)
		}

		return 0, ErrTokenUnknown
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET used = 1 WHERE token_hash = ?;`, hash); err != nil {
		return 0, fmt.Errorf("mark token used: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("redeem refresh token: commit: %w", err)
	}

	return userID, nil
}

func (s *Store) RevokeUserTokens(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?;`, userID); err != nil {
		return fmt.Errorf("revoke tokens of user %d: %w", userID, err)
	}

	return nil
}

//
// AUDIT
//

func (s *Store) Audit(ctx context.Context, actorID *int64, action, detail, ip string) {
	// Audit failures must never break the request they describe, so this
	// logs and moves on rather than returning an error.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (actor_id, action, detail, ip) VALUES (?, ?, ?, ?);`,
		actorID, action, detail, ip); err != nil {
		log.Printf("audit write failed action=%s: %v", action, err)
	}
}

func (s *Store) AuditTail(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor_id, action, detail, ip, created_at
		 FROM audit_log ORDER BY id DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	defer closeRows(rows)

	entries := make([]AuditEntry, 0, limit)

	for rows.Next() {
		var (
			entry   AuditEntry
			actorID sql.NullInt64
		)

		if err := rows.Scan(&entry.ID, &actorID, &entry.Action, &entry.Detail, &entry.IP, &entry.At); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}

		if actorID.Valid {
			id := actorID.Int64
			entry.ActorID = &id
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	return entries, nil
}

//
// DATABASE
//

func OpenDB(ctx context.Context, path string) (*sql.DB, error) {
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

	if _, err := db.ExecContext(ctx, Schema); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}

		return nil, fmt.Errorf("create schema: %w", err)
	}

	return db, nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("close rows: %v", err)
	}
}
