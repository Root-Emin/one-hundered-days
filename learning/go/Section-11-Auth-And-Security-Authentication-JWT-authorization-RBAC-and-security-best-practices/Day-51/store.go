package main

import (
	"context"
	"crypto/rand"
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
Users and sessions.

Two storage rules worth stating out loud:

  - the users table holds a password *hash*, and the column is never selected
    into anything that gets logged or serialized
  - the sessions table holds a *hash of the session token*, not the token.
    A leaked database must not hand the attacker working sessions, exactly the
    same reasoning as for passwords.
*/

var (
	ErrEmailTaken   = errors.New("email already registered")
	ErrUserNotFound = errors.New("user not found")
	ErrNoSession    = errors.New("session is missing or expired")
)

const Schema = `
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	email         TEXT NOT NULL,
	display_name  TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (email);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);`

type User struct {
	ID          int64
	Email       string
	DisplayName string
	CreatedAt   time.Time

	// passwordHash is unexported so it cannot be marshalled by accident.
	passwordHash string
}

type Session struct {
	UserID    int64
	ExpiresAt time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateUser(ctx context.Context, email, displayName, passwordHash string) (User, error) {
	email = normalizeEmail(email)

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES (?, ?, ?);`,
		email, strings.TrimSpace(displayName), passwordHash)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return User{}, fmt.Errorf("register %s: %w", email, ErrEmailTaken)
		}

		return User{}, fmt.Errorf("register %s: %w", email, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("register %s: read id: %w", email, err)
	}

	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, created_at FROM users WHERE id = ?;`, id),
		fmt.Sprintf("user %d", id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, created_at FROM users WHERE email = ?;`,
		normalizeEmail(email)),
		"user by email")
}

func (s *Store) scanUser(row *sql.Row, subject string) (User, error) {
	var (
		user      User
		createdAt string
	)

	err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &user.passwordHash, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return User{}, fmt.Errorf("%s: %w", subject, ErrUserNotFound)
	case err != nil:
		return User{}, fmt.Errorf("lookup %s: %w", subject, err)
	}

	user.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return User{}, fmt.Errorf("parse created_at: %w", err)
	}

	return user, nil
}

//
// SESSIONS
//

// NewSessionToken returns the token handed to the client and the hash stored
// in the database.
//
// 32 bytes from crypto/rand is 256 bits of entropy: not guessable, and short
// enough to fit comfortably in a header. The database only ever sees the
// SHA-256 of it.
func NewSessionToken() (token, hash string, err error) {
	raw := make([]byte, 32)

	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)

	return token, hashToken(token), nil
}

// hashToken is a plain SHA-256, not bcrypt: the input is already 256 random
// bits, so there is nothing to brute force and the lookup must be fast.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, time.Time, error) {
	token, hash, err := NewSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}

	expiresAt := time.Now().UTC().Add(ttl)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?);`,
		hash, userID, expiresAt.Format(time.DateTime)); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}

	return token, expiresAt, nil
}

// SessionByToken looks up a session and enforces expiry in the query, so an
// expired row can never authenticate a request even if cleanup is late.
func (s *Store) SessionByToken(ctx context.Context, token string) (Session, error) {
	var (
		session   Session
		expiresAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM sessions
		 WHERE token_hash = ? AND expires_at > datetime('now');`,
		hashToken(token)).Scan(&session.UserID, &expiresAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Session{}, ErrNoSession
	case err != nil:
		return Session{}, fmt.Errorf("lookup session: %w", err)
	}

	session.ExpiresAt, err = time.Parse(time.DateTime, expiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse expires_at: %w", err)
	}

	return session, nil
}

// DeleteSession is logout. Server-side sessions can be revoked instantly,
// which is the main thing they have over stateless tokens (Day 52).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE token_hash = ?;`, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// DeleteUserSessions is what "log out everywhere" and "password changed" run.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?;`, userID); err != nil {
		return fmt.Errorf("delete sessions of user %d: %w", userID, err)
	}

	return nil
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= datetime('now');`)
	if err != nil {
		return 0, fmt.Errorf("purge sessions: %w", err)
	}

	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge sessions: rows affected: %w", err)
	}

	return removed, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func OpenDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
