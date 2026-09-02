package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
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
Day 42 - Databases (I): Migrations and Schema Design

Tasks covered:

 1. Migration tool: versioned .sql files checked into the repo, applied by the
    small runner in this file (same model as goose / golang-migrate)
 2. Table design: primary keys, timestamps and indexes from day one
 3. Reversible up/down migrations
 4. Running migrations in dev and verifying the resulting schema

Run:

	go run main.go status      # what is applied, what is pending
	go run main.go up          # apply all pending migrations
	go run main.go down        # revert the newest applied migration
	go run main.go down -n 2   # revert the newest two
	go run main.go redo        # down + up on the newest migration
	go run main.go schema      # dump the schema the database actually has

Environment variables:

	DB_PATH  SQLite file path. Default: ./data/day42.db

Migration files live in ./migrations and are named:

	<version>_<name>.up.sql
	<version>_<name>.down.sql

Rules the runner enforces:
  - versions apply in ascending order, never out of order
  - each migration runs inside its own transaction
  - a migration is recorded only if its statements committed
  - every up file must have a matching down file (reversibility is checked,
    not assumed)
*/

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	migrationsDir = "migrations"
	defaultDBPath = "data/day42.db"
	stepTimeout   = 30 * time.Second
)

//
// MIGRATION MODEL
//

type Migration struct {
	Version int64
	Name    string
	Up      string
	Down    string
}

func (m Migration) Label() string {
	return fmt.Sprintf("%04d_%s", m.Version, m.Name)
}

// loadMigrations reads the embedded .sql files and pairs up/down halves.
//
// Embedding matters in production: the binary carries its own migrations, so
// a deployed container can never drift from the SQL it was built with.
func loadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	byVersion := make(map[int64]*Migration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		var direction string

		switch {
		case strings.HasSuffix(name, ".up.sql"):
			direction = "up"
		case strings.HasSuffix(name, ".down.sql"):
			direction = "down"
		default:
			return nil, fmt.Errorf("migration %q must end in .up.sql or .down.sql", name)
		}

		base := strings.TrimSuffix(name, "."+direction+".sql")

		version, label, found := strings.Cut(base, "_")
		if !found {
			return nil, fmt.Errorf("migration %q must be named <version>_<name>.%s.sql", name, direction)
		}

		parsed, err := strconv.ParseInt(version, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %q has a non-numeric version: %w", name, err)
		}

		content, err := fs.ReadFile(migrationFS, filepath.ToSlash(filepath.Join(migrationsDir, name)))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}

		migration, ok := byVersion[parsed]
		if !ok {
			migration = &Migration{Version: parsed, Name: label}
			byVersion[parsed] = migration
		}

		if direction == "up" {
			migration.Up = string(content)
		} else {
			migration.Down = string(content)
		}
	}

	migrations := make([]Migration, 0, len(byVersion))

	for _, migration := range byVersion {
		// A migration you cannot revert is a migration you cannot experiment
		// with. Refuse to run a half-defined pair.
		if strings.TrimSpace(migration.Up) == "" {
			return nil, fmt.Errorf("migration %s has no .up.sql", migration.Label())
		}

		if strings.TrimSpace(migration.Down) == "" {
			return nil, fmt.Errorf("migration %s has no .down.sql", migration.Label())
		}

		migrations = append(migrations, *migration)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

//
// MIGRATION STATE TABLE
//

// The runner tracks its own state in the database it manages, so any
// environment can answer "which version am I on?" without a side channel.
const createStateTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

func ensureStateTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createStateTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int64]time.Time, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version;`)
	if err != nil {
		return nil, fmt.Errorf("select applied migrations: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("close rows: %v", err)
		}
	}()

	applied := make(map[int64]time.Time)

	for rows.Next() {
		var (
			version   int64
			appliedAt string
		)

		if err := rows.Scan(&version, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}

		parsed, err := time.Parse(time.DateTime, appliedAt)
		if err != nil {
			return nil, fmt.Errorf("parse applied_at %q: %w", appliedAt, err)
		}

		applied[version] = parsed
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}

	return applied, nil
}

//
// APPLY / REVERT
//

// runStep executes one migration half plus its bookkeeping inside a single
// transaction: either the schema change and the version row both land, or
// neither does. A crash mid-migration can never leave a lying state table.
func runStep(ctx context.Context, db *sql.DB, migration Migration, up bool) error {
	ctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// Rollback is a no-op once Commit succeeded, so this defer is always safe.
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("rollback %s: %v", migration.Label(), err)
		}
	}()

	body := migration.Up
	if !up {
		body = migration.Down
	}

	for _, statement := range splitStatements(body) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("%s: exec %q: %w", migration.Label(), firstLine(statement), err)
		}
	}

	if up {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?);`,
			migration.Version,
			migration.Name,
		)
	} else {
		_, err = tx.ExecContext(
			ctx,
			`DELETE FROM schema_migrations WHERE version = ?;`,
			migration.Version,
		)
	}

	if err != nil {
		return fmt.Errorf("%s: record version: %w", migration.Label(), err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", migration.Label(), err)
	}

	return nil
}

// splitStatements breaks a file into individual statements, because
// database/sql drivers execute one statement per call.
//
// Whole-line "--" comments are stripped first: a semicolon inside a comment
// (";" in a sentence, for example) would otherwise split a statement in half.
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

func firstLine(statement string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(statement), "\n")

	return line
}

//
// COMMANDS
//

func commandUp(ctx context.Context, db *sql.DB, migrations []Migration) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	pending := 0

	for _, migration := range migrations {
		if _, done := applied[migration.Version]; done {
			continue
		}

		log.Printf("applying %s", migration.Label())

		if err := runStep(ctx, db, migration, true); err != nil {
			return err
		}

		pending++
	}

	if pending == 0 {
		log.Printf("database already up to date")
		return nil
	}

	log.Printf("applied %d migration(s)", pending)

	return nil
}

func commandDown(ctx context.Context, db *sql.DB, migrations []Migration, steps int) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	reverted := 0

	// Newest first: a schema is unwound in the reverse order it was built.
	for i := len(migrations) - 1; i >= 0 && reverted < steps; i-- {
		migration := migrations[i]

		if _, done := applied[migration.Version]; !done {
			continue
		}

		log.Printf("reverting %s", migration.Label())

		if err := runStep(ctx, db, migration, false); err != nil {
			return err
		}

		reverted++
	}

	if reverted == 0 {
		log.Printf("nothing to revert")
		return nil
	}

	log.Printf("reverted %d migration(s)", reverted)

	return nil
}

func commandStatus(ctx context.Context, db *sql.DB, migrations []Migration) error {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	fmt.Printf("\n%-8s %-26s %-9s %s\n", "VERSION", "NAME", "STATE", "APPLIED AT")
	fmt.Println(strings.Repeat("-", 66))

	for _, migration := range migrations {
		appliedAt, done := applied[migration.Version]

		state := "pending"
		stamp := "-"

		if done {
			state = "applied"
			stamp = appliedAt.Format(time.DateTime)
		}

		fmt.Printf("%-8d %-26s %-9s %s\n", migration.Version, migration.Name, state, stamp)
	}

	fmt.Printf("\n%d migration(s) known, %d applied\n\n", len(migrations), len(applied))

	return nil
}

// commandSchema verifies the real schema instead of trusting the files: this
// is the "does the schema match expectations?" step of today's tasks.
func commandSchema(ctx context.Context, db *sql.DB) error {
	const query = `
		SELECT type, name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END, name;`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("close rows: %v", err)
		}
	}()

	fmt.Println()

	for rows.Next() {
		var kind, name, ddl string

		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			return fmt.Errorf("scan schema row: %w", err)
		}

		if ddl == "" {
			// Auto-created objects (for example AUTOINCREMENT bookkeeping)
			// have no DDL of their own.
			fmt.Printf("%-6s %s (implicit)\n\n", kind, name)
			continue
		}

		fmt.Printf("%-6s %s\n%s;\n\n", kind, name, strings.TrimSpace(ddl))
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema rows: %w", err)
	}

	return nil
}

//
// WIRING
//

func openDB(ctx context.Context) (*sql.DB, error) {
	path := os.Getenv("DB_PATH")

	if path == "" {
		path = defaultDBPath
	}

	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	// Foreign keys are OFF by default in SQLite; the notes -> users reference
	// is only enforced with this pragma on.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
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

	log.Printf("connected dsn=%s", path)

	return db, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run main.go <up|down|redo|status|schema> [-n steps]")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day42: ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	command := os.Args[1]

	flags := flag.NewFlagSet(command, flag.ExitOnError)
	steps := flags.Int("n", 1, "number of migrations to revert")

	if err := flags.Parse(os.Args[2:]); err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	if *steps < 1 {
		log.Fatalf("-n must be at least 1")
	}

	ctx := context.Background()

	migrations, err := loadMigrations()
	if err != nil {
		log.Fatalf("load migrations: %v", err)
	}

	db, err := openDB(ctx)
	if err != nil {
		log.Fatalf("database unavailable: %v", err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	if err := ensureStateTable(ctx, db); err != nil {
		log.Fatalf("prepare migration state: %v", err)
	}

	switch command {
	case "up":
		err = commandUp(ctx, db, migrations)

	case "down":
		err = commandDown(ctx, db, migrations, *steps)

	case "redo":
		if err = commandDown(ctx, db, migrations, 1); err == nil {
			err = commandUp(ctx, db, migrations)
		}

	case "status":
		err = commandStatus(ctx, db, migrations)

	case "schema":
		err = commandSchema(ctx, db)

	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		log.Fatalf("%s: %v", command, err)
	}
}
