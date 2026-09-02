package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/*
Database plumbing: pool settings, migrations, and the transaction manager.

The pool numbers here are the ones documented in README.md. Keeping them in
one place (rather than sprinkled through main) is what makes them reviewable.
*/

//go:embed migrations/*.sql
var migrationFS embed.FS

type DBConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultDBConfig() DBConfig {
	return DBConfig{
		Path:            envOr("DB_PATH", "data/shop.db"),
		MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 10),
		ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		ConnMaxIdleTime: envDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}
}

func OpenDB(ctx context.Context, config DBConfig) (*sql.DB, error) {
	if config.Path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	// foreign_keys: SQLite ignores REFERENCES without it.
	// journal_mode WAL + busy_timeout: readers do not block on the writer,
	// and a blocked writer waits instead of failing immediately.
	dsn := config.Path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close database after failed ping: %v", closeErr)
		}

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}

//
// TRANSACTION MANAGER
//

type SQLTxManager struct {
	db *sql.DB
}

func NewSQLTxManager(db *sql.DB) *SQLTxManager {
	return &SQLTxManager{db: db}
}

var _ TxManager = (*SQLTxManager)(nil)

// WithinTx opens a transaction, builds repositories bound to it, and commits
// only if fn returns nil.
//
// The service above never sees *sql.Tx: it receives Repositories that happen
// to share one. That is what keeps the transaction boundary in the data layer
// where it belongs, instead of leaking into business code.
//
// Concurrent writers can lose a race for the write lock (SQLITE_BUSY here,
// serialization_failure 40001 on Postgres SERIALIZABLE, deadlock 40P01 on
// either). Those are not application errors: the correct response is to run
// the whole unit of work again, which is why fn must be safe to retry.
func (m *SQLTxManager) WithinTx(ctx context.Context, fn func(repos Repositories) error) error {
	var lastErr error

	for attempt := 1; attempt <= maxTxAttempts; attempt++ {
		err := m.runTx(ctx, fn)

		switch {
		case err == nil:
			return nil

		case !isRetryable(err):
			// A business error, or a bug. Retrying would only repeat it.
			return err
		}

		lastErr = err

		// Exponential backoff with a cap, so a burst of writers spreads out
		// instead of all retrying in lockstep.
		backoff := time.Duration(attempt*attempt) * 2 * time.Millisecond

		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case <-time.After(backoff):
		}
	}

	return fmt.Errorf("transaction failed after %d attempts: %w", maxTxAttempts, lastErr)
}

// maxTxAttempts bounds the retry loop: a permanently contended write must
// surface as an error, not as a request that never returns.
const maxTxAttempts = 8

func (m *SQLTxManager) runTx(ctx context.Context, fn func(repos Repositories) error) (err error) {
	tx, beginErr := m.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return fmt.Errorf("begin transaction: %w", beginErr)
	}

	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr))
		}
	}()

	if err := fn(RepositoriesFor(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// isRetryable recognises lock and serialization conflicts. With pgx this
// would check pgErr.Code against "40001" and "40P01" instead of matching text.
func isRetryable(err error) bool {
	message := strings.ToLower(err.Error())

	for _, marker := range []string{
		"database is locked", // SQLITE_BUSY
		"database table is locked",
		"sqlite_busy",
		"serialization failure", // Postgres 40001
		"deadlock detected",     // Postgres 40P01
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

// RepositoriesFor builds the repository set over any handle: the pool for
// single-statement reads, a transaction for units of work.
func RepositoriesFor(db dbtx) Repositories {
	return Repositories{
		Customers: NewSQLCustomerRepository(db),
		Products:  NewSQLProductRepository(db),
		Orders:    NewSQLOrderRepository(db),
	}
}

//
// MIGRATIONS
//

type migration struct {
	version int64
	name    string
	up      string
	down    string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	byVersion := make(map[int64]*migration)

	for _, entry := range entries {
		name := entry.Name()

		direction := "up"
		if strings.HasSuffix(name, ".down.sql") {
			direction = "down"
		}

		base := strings.TrimSuffix(name, "."+direction+".sql")

		versionText, label, found := strings.Cut(base, "_")
		if !found {
			return nil, fmt.Errorf("migration %q must be <version>_<name>.<up|down>.sql", name)
		}

		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", name, err)
		}

		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}

		item, ok := byVersion[version]
		if !ok {
			item = &migration{version: version, name: label}
			byVersion[version] = item
		}

		if direction == "up" {
			item.up = string(body)
		} else {
			item.down = string(body)
		}
	}

	migrations := make([]migration, 0, len(byVersion))

	for _, item := range byVersion {
		if strings.TrimSpace(item.up) == "" || strings.TrimSpace(item.down) == "" {
			return nil, fmt.Errorf("migration %04d_%s is missing a half", item.version, item.name)
		}

		migrations = append(migrations, *item)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func MigrateUp(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	for _, item := range migrations {
		if applied[item.version] {
			continue
		}

		log.Printf("migrate up %04d_%s", item.version, item.name)

		if err := runMigration(ctx, db, item, true); err != nil {
			return err
		}
	}

	return nil
}

func MigrateDown(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	for i := len(migrations) - 1; i >= 0; i-- {
		if !applied[migrations[i].version] {
			continue
		}

		log.Printf("migrate down %04d_%s", migrations[i].version, migrations[i].name)

		return runMigration(ctx, db, migrations[i], false)
	}

	log.Printf("nothing to revert")

	return nil
}

func MigrationStatus(ctx context.Context, db *sql.DB) error {
	migrations, applied, err := migrationState(ctx, db)
	if err != nil {
		return err
	}

	fmt.Printf("\n%-9s %-16s %s\n", "VERSION", "NAME", "STATE")
	fmt.Println(strings.Repeat("-", 40))

	for _, item := range migrations {
		state := "pending"
		if applied[item.version] {
			state = "applied"
		}

		fmt.Printf("%-9d %-16s %s\n", item.version, item.name, state)
	}

	fmt.Println()

	return nil
}

func migrationState(ctx context.Context, db *sql.DB) ([]migration, map[int64]bool, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, nil, err
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`); err != nil {
		return nil, nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	defer closeRows(rows)

	applied := make(map[int64]bool)

	for rows.Next() {
		var version int64

		if err := rows.Scan(&version); err != nil {
			return nil, nil, fmt.Errorf("scan version: %w", err)
		}

		applied[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate versions: %w", err)
	}

	return migrations, applied, nil
}

func runMigration(ctx context.Context, db *sql.DB, item migration, up bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %04d: begin: %w", item.version, err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("migration %04d: rollback: %v", item.version, err)
		}
	}()

	body := item.up
	if !up {
		body = item.down
	}

	for _, statement := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %04d: %w", item.version, err)
		}
	}

	if up {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?);`, item.version, item.name)
	} else {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM schema_migrations WHERE version = ?;`, item.version)
	}

	if err != nil {
		return fmt.Errorf("migration %04d: record version: %w", item.version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %04d: commit: %w", item.version, err)
	}

	return nil
}

// splitStatements strips whole-line comments before splitting, because a
// semicolon inside a comment would otherwise cut a statement in half.
func splitStatements(body string) []string {
	var stripped strings.Builder

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}

		stripped.WriteString(line)
		stripped.WriteString("\n")
	}

	parts := strings.Split(stripped.String(), ";")
	statements := make([]string, 0, len(parts))

	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}

		statements = append(statements, strings.TrimSpace(part)+";")
	}

	return statements
}

//
// ENV
//

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %d", key, value, fallback)
		return fallback
	}

	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %s", key, value, fallback)
		return fallback
	}

	return parsed
}
