package main

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

/*
The repository under test.

Note the DBTX interface: every method takes its handle from the struct, and
that handle can be a *sql.DB or a *sql.Tx. That one indirection is what lets a
test wrap each case in a transaction and roll it back afterwards - the fastest
reliable way to reset state between database tests.
*/

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

// DBTX is satisfied by both *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const Schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	email      TEXT    NOT NULL UNIQUE,
	plan       TEXT    NOT NULL DEFAULT 'free',
	created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS invoices (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id  INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
	amount_cent INTEGER NOT NULL CHECK (amount_cent > 0),
	paid        INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_invoices_account ON invoices (account_id, paid);`

// TableNames is used by the truncate-based reset strategy. Order matters:
// children before parents, or foreign keys reject the delete.
var TableNames = []string{"invoices", "accounts"}

type Account struct {
	ID        int64
	Email     string
	Plan      string
	CreatedAt time.Time
}

type Invoice struct {
	ID         int64
	AccountID  int64
	AmountCent int64
	Paid       bool
}

type Store struct {
	db DBTX
}

// NewStore accepts anything that can run statements, which is what makes the
// transaction-rollback test strategy possible.
func NewStore(db DBTX) *Store {
	return &Store{db: db}
}

func (s *Store) CreateAccount(ctx context.Context, email, plan string) (Account, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if email == "" || !strings.Contains(email, "@") {
		return Account{}, fmt.Errorf("create account: invalid email %q", email)
	}

	if plan == "" {
		plan = "free"
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (email, plan) VALUES (?, ?);`, email, plan)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED") {
			return Account{}, fmt.Errorf("create account %s: %w", email, ErrDuplicate)
		}

		return Account{}, fmt.Errorf("create account %s: %w", email, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Account{}, fmt.Errorf("create account %s: read id: %w", email, err)
	}

	return s.AccountByID(ctx, id)
}

func (s *Store) AccountByID(ctx context.Context, id int64) (Account, error) {
	var (
		account   Account
		createdAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, plan, created_at FROM accounts WHERE id = ?;`, id).
		Scan(&account.ID, &account.Email, &account.Plan, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("account %d: %w", id, ErrNotFound)
	case err != nil:
		return Account{}, fmt.Errorf("select account %d: %w", id, err)
	}

	account.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return account, nil
}

func (s *Store) AccountByEmail(ctx context.Context, email string) (Account, error) {
	var (
		account   Account
		createdAt string
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, plan, created_at FROM accounts WHERE email = ?;`,
		strings.ToLower(strings.TrimSpace(email))).
		Scan(&account.ID, &account.Email, &account.Plan, &createdAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("account %s: %w", email, ErrNotFound)
	case err != nil:
		return Account{}, fmt.Errorf("select account %s: %w", email, err)
	}

	account.CreatedAt, err = time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return account, nil
}

func (s *Store) CreateInvoice(ctx context.Context, accountID, amountCent int64) (Invoice, error) {
	if amountCent <= 0 {
		return Invoice{}, fmt.Errorf("create invoice: amount must be positive, got %d", amountCent)
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO invoices (account_id, amount_cent) VALUES (?, ?);`, accountID, amountCent)
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY CONSTRAINT FAILED") {
			return Invoice{}, fmt.Errorf("create invoice for account %d: %w", accountID, ErrNotFound)
		}

		return Invoice{}, fmt.Errorf("create invoice for account %d: %w", accountID, err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Invoice{}, fmt.Errorf("create invoice: read id: %w", err)
	}

	return Invoice{ID: id, AccountID: accountID, AmountCent: amountCent}, nil
}

func (s *Store) MarkInvoicePaid(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE invoices SET paid = 1 WHERE id = ? AND paid = 0;`, id)
	if err != nil {
		return fmt.Errorf("mark invoice %d paid: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark invoice %d paid: rows affected: %w", id, err)
	}

	if affected == 0 {
		// Already paid or missing: check which, so the caller gets the truth.
		var exists int

		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM invoices WHERE id = ?;`, id).Scan(&exists); err != nil {
			return fmt.Errorf("mark invoice %d paid: %w", id, err)
		}

		if exists == 0 {
			return fmt.Errorf("invoice %d: %w", id, ErrNotFound)
		}
	}

	return nil
}

func (s *Store) UnpaidTotal(ctx context.Context, accountID int64) (int64, error) {
	var total int64

	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_cent), 0) FROM invoices WHERE account_id = ? AND paid = 0;`,
		accountID).Scan(&total); err != nil {
		return 0, fmt.Errorf("unpaid total for account %d: %w", accountID, err)
	}

	return total, nil
}

func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}

	return count, nil
}

//
// DATABASE HELPERS
//

func OpenDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

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

// Truncate empties every table without dropping the schema. On Postgres this
// would be "TRUNCATE invoices, accounts RESTART IDENTITY CASCADE"; SQLite has
// no TRUNCATE, so DELETE plus a sequence reset is the equivalent.
func Truncate(ctx context.Context, db *sql.DB) error {
	for _, table := range TableNames {
		if _, err := db.ExecContext(ctx, `DELETE FROM `+table+`;`); err != nil {
			return fmt.Errorf("truncate %s: %w", table, err)
		}
	}

	// Reset AUTOINCREMENT counters so ids start at 1 again and tests can
	// assert on them without depending on execution order.
	if _, err := db.ExecContext(ctx, `DELETE FROM sqlite_sequence;`); err != nil {
		return fmt.Errorf("reset sequences: %w", err)
	}

	return nil
}
