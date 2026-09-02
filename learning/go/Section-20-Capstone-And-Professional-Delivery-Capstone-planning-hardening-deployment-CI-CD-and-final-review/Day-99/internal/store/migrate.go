package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// migrationFiles carries the schema inside the binary, so a deployed container
// does not need the repository checked out beside it.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one versioned pair of scripts.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

const schemaTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

// LoadMigrations reads the embedded migrations, newest last.
//
// A missing down script is an error rather than a warning: a migration with no
// way back is a deploy with no way back.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	byVersion := make(map[int]*Migration)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, name, direction, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}

		content, err := fs.ReadFile(migrationFiles, path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		migration, found := byVersion[version]
		if !found {
			migration = &Migration{Version: version, Name: name}
			byVersion[version] = migration
		}

		if direction == "up" {
			migration.Up = string(content)
		} else {
			migration.Down = string(content)
		}
	}

	migrations := make([]Migration, 0, len(byVersion))

	for _, migration := range byVersion {
		if migration.Up == "" || migration.Down == "" {
			return nil, fmt.Errorf("migration %d (%s) is missing an up or down script",
				migration.Version, migration.Name)
		}

		migrations = append(migrations, *migration)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })

	return migrations, nil
}

func parseMigrationName(fileName string) (version int, name, direction string, err error) {
	base := strings.TrimSuffix(fileName, ".sql")

	switch {
	case strings.HasSuffix(base, ".up"):
		direction = "up"
	case strings.HasSuffix(base, ".down"):
		direction = "down"
	default:
		return 0, "", "", fmt.Errorf("%s: expected <version>_<name>.up.sql or .down.sql", fileName)
	}

	base = strings.TrimSuffix(strings.TrimSuffix(base, ".up"), ".down")

	number, rest, found := strings.Cut(base, "_")
	if !found {
		return 0, "", "", fmt.Errorf("%s: no version prefix", fileName)
	}

	if version, err = strconv.Atoi(number); err != nil {
		return 0, "", "", fmt.Errorf("%s: version %q is not a number", fileName, number)
	}

	return version, rest, direction, nil
}

// Migrate applies every migration that has not run yet.
//
// Each migration and its bookkeeping row commit in ONE transaction: a crash
// mid-way leaves the database at a known version rather than half-applied.
func Migrate(ctx context.Context, db *sql.DB) ([]Migration, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, schemaTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	var ran []Migration

	for _, migration := range migrations {
		if applied[migration.Version] {
			continue
		}

		if err := applyMigration(ctx, db, migration); err != nil {
			return ran, err
		}

		ran = append(ran, migration)
	}

	return ran, nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
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

	for _, statement := range splitStatements(migration.Up) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES (?, ?);`,
		migration.Version, migration.Name); err != nil {
		return fmt.Errorf("record migration %d: %w", migration.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}

	committed = true

	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, fmt.Errorf("select applied versions: %w", err)
	}

	defer closeRows(rows)

	applied := make(map[int]bool)

	for rows.Next() {
		var version int

		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}

		applied[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate versions: %w", err)
	}

	return applied, nil
}

// splitStatements strips whole-line "--" comments BEFORE splitting on ";",
// because a semicolon inside a comment would cut a statement in half.
func splitStatements(script string) []string {
	var cleaned []string

	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}

		cleaned = append(cleaned, line)
	}

	var statements []string

	for _, statement := range strings.Split(strings.Join(cleaned, "\n"), ";") {
		if trimmed := strings.TrimSpace(statement); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}

	return statements
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		_ = err
	}
}
