// Package store is a small repository that runs on both SQLite and Postgres.
//
// Supporting two engines is not the lesson; it is what makes the lesson
// possible. The test helper in internal/testenv runs the same suite against a
// throwaway SQLite file when Docker is absent and against a real Postgres
// container when it is available, so a developer on a plane and CI on a
// runner execute the same assertions.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotFound  = errors.New("account not found")
	ErrDuplicate = errors.New("email already registered")
)

// Dialect is the difference between the two engines, in one place.
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

type Account struct {
	ID        int64
	Email     string
	Plan      string
	CreatedAt time.Time
}

type Store struct {
	db      *sql.DB
	dialect Dialect
	// schema is a Postgres schema name, so parallel tests can each own one.
	// Empty for SQLite, where a separate file plays the same role.
	schema string
}

func New(db *sql.DB, dialect Dialect, schema string) *Store {
	return &Store{db: db, dialect: dialect, schema: schema}
}

func (s *Store) Dialect() Dialect { return s.dialect }

// table qualifies the table name with the schema when there is one.
func (s *Store) table() string {
	if s.schema == "" {
		return "accounts"
	}

	return s.schema + ".accounts"
}

// rebind turns the ? placeholders used throughout this file into $1, $2 for
// Postgres. Writing every query twice would be the alternative, and the two
// copies would drift within a month.
func (s *Store) rebind(query string) string {
	if s.dialect != Postgres {
		return query
	}

	var builder strings.Builder

	argument := 0

	for _, char := range query {
		if char == '?' {
			argument++

			builder.WriteString("$" + strconv.Itoa(argument))

			continue
		}

		builder.WriteRune(char)
	}

	return builder.String()
}

// Migrate creates the schema for whichever engine is behind the handle.
func (s *Store) Migrate(ctx context.Context) error {
	statements := s.migrationStatements()

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	return nil
}

func (s *Store) migrationStatements() []string {
	if s.dialect == Postgres {
		statements := make([]string, 0, 3)

		if s.schema != "" {
			statements = append(statements, `CREATE SCHEMA IF NOT EXISTS `+s.schema+`;`)
		}

		statements = append(statements,
			`CREATE TABLE IF NOT EXISTS `+s.table()+` (
				id         BIGSERIAL PRIMARY KEY,
				email      TEXT        NOT NULL,
				plan       TEXT        NOT NULL DEFAULT 'free',
				created_at TIMESTAMPTZ NOT NULL DEFAULT now()
			);`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_email_`+indexSuffix(s.schema)+
				` ON `+s.table()+` (email);`,
		)

		return statements
	}

	return []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			email      TEXT NOT NULL,
			plan       TEXT NOT NULL DEFAULT 'free',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_email ON accounts (email);`,
	}
}

func indexSuffix(schema string) string {
	if schema == "" {
		return "public"
	}

	return schema
}

func (s *Store) Create(ctx context.Context, email, plan string) (Account, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if email == "" || !strings.Contains(email, "@") {
		return Account{}, fmt.Errorf("create account: invalid email %q", email)
	}

	if plan == "" {
		plan = "free"
	}

	var id int64

	if s.dialect == Postgres {
		// RETURNING is the Postgres way to learn the generated id.
		err := s.db.QueryRowContext(ctx,
			s.rebind(`INSERT INTO `+s.table()+` (email, plan) VALUES (?, ?) RETURNING id;`),
			email, plan).Scan(&id)
		if err != nil {
			if isDuplicate(err) {
				return Account{}, fmt.Errorf("create account %s: %w", email, ErrDuplicate)
			}

			return Account{}, fmt.Errorf("create account %s: %w", email, err)
		}
	} else {
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO accounts (email, plan) VALUES (?, ?);`, email, plan)
		if err != nil {
			if isDuplicate(err) {
				return Account{}, fmt.Errorf("create account %s: %w", email, ErrDuplicate)
			}

			return Account{}, fmt.Errorf("create account %s: %w", email, err)
		}

		if id, err = result.LastInsertId(); err != nil {
			return Account{}, fmt.Errorf("create account %s: read id: %w", email, err)
		}
	}

	return s.ByID(ctx, id)
}

func (s *Store) ByID(ctx context.Context, id int64) (Account, error) {
	account, err := s.scan(s.db.QueryRowContext(ctx,
		s.rebind(`SELECT id, email, plan, created_at FROM `+s.table()+` WHERE id = ?;`), id))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("account %d: %w", id, ErrNotFound)
	case err != nil:
		return Account{}, fmt.Errorf("select account %d: %w", id, err)
	}

	return account, nil
}

func (s *Store) ByEmail(ctx context.Context, email string) (Account, error) {
	account, err := s.scan(s.db.QueryRowContext(ctx,
		s.rebind(`SELECT id, email, plan, created_at FROM `+s.table()+` WHERE email = ?;`),
		strings.ToLower(strings.TrimSpace(email))))

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("account %s: %w", email, ErrNotFound)
	case err != nil:
		return Account{}, fmt.Errorf("select account %s: %w", email, err)
	}

	return account, nil
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.table()+`;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}

	return count, nil
}

func (s *Store) Truncate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM `+s.table()+`;`); err != nil {
		return fmt.Errorf("truncate accounts: %w", err)
	}

	if s.dialect == SQLite {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM sqlite_sequence WHERE name = 'accounts';`); err != nil {
			return fmt.Errorf("reset sequence: %w", err)
		}
	}

	return nil
}

// DropSchema is the Postgres teardown: it removes everything a test created,
// so a shared container does not accumulate junk across a long CI run.
func (s *Store) DropSchema(ctx context.Context) error {
	if s.dialect != Postgres || s.schema == "" {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+s.schema+` CASCADE;`); err != nil {
		return fmt.Errorf("drop schema %s: %w", s.schema, err)
	}

	return nil
}

func (s *Store) scan(row *sql.Row) (Account, error) {
	var account Account

	if s.dialect == Postgres {
		if err := row.Scan(&account.ID, &account.Email, &account.Plan, &account.CreatedAt); err != nil {
			return Account{}, err
		}

		return account, nil
	}

	var createdAt string

	if err := row.Scan(&account.ID, &account.Email, &account.Plan, &createdAt); err != nil {
		return Account{}, err
	}

	parsed, err := time.Parse(time.DateTime, createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	account.CreatedAt = parsed.UTC()

	return account, nil
}

// isDuplicate hides the one genuinely engine-specific error check.
func isDuplicate(err error) bool {
	message := strings.ToUpper(err.Error())

	return strings.Contains(message, "UNIQUE CONSTRAINT FAILED") || // SQLite
		strings.Contains(message, "SQLSTATE 23505") || // pgx
		strings.Contains(message, "DUPLICATE KEY VALUE") // Postgres text
}
