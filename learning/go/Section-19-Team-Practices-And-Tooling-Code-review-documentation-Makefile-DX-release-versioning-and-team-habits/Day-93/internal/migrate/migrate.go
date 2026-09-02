// Package migrate applies versioned SQL migrations from an embedded
// filesystem.
//
// Embedded, not read from disk: the binary that runs in production must not
// need the repository checked out beside it. `make migrate` and the deployed
// container run exactly the same bytes.
//
// The rules this enforces are the ones teams learn by breaking them:
//
//   - migrations are numbered and applied in order, once each
//   - every up file has a down file, so a bad deploy has a way back
//   - each migration runs in a transaction with its own bookkeeping row, so a
//     failure leaves the database at a known version rather than half-way
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Migration is one versioned pair of scripts.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// Status is one row of the migration report.
type Status struct {
	Version   int
	Name      string
	Applied   bool
	AppliedAt string
}

const schemaTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

// Load reads migrations from a filesystem.
//
// It takes an fs.FS rather than an embed.FS so the same function serves the
// embedded production path, a directory during development, and fstest.MapFS
// in a test. Accepting the interface costs nothing and removes the need for
// fixture files on disk.
//
// File names are <version>_<name>.<up|down>.sql - the same convention
// golang-migrate uses, so moving to it later is a rename away.
func Load(files fs.FS, directory string) ([]Migration, error) {
	entries, err := fs.ReadDir(files, directory)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", directory, err)
	}

	byVersion := make(map[int]*Migration)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, name, direction, err := parseName(entry.Name())
		if err != nil {
			return nil, err
		}

		content, err := fs.ReadFile(files, path.Join(directory, entry.Name()))
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
		if migration.Up == "" {
			return nil, fmt.Errorf("migration %d (%s) has no up script", migration.Version, migration.Name)
		}

		// A migration with no way back is a deploy with no way back. Making
		// this an error - rather than a warning nobody reads - is the point.
		if migration.Down == "" {
			return nil, fmt.Errorf("migration %d (%s) has no down script", migration.Version, migration.Name)
		}

		migrations = append(migrations, *migration)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })

	return migrations, nil
}

func parseName(fileName string) (version int, name, direction string, err error) {
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

	version, err = strconv.Atoi(number)
	if err != nil {
		return 0, "", "", fmt.Errorf("%s: version %q is not a number", fileName, number)
	}

	return version, rest, direction, nil
}

// Up applies every migration that has not been applied yet.
func Up(ctx context.Context, db *sql.DB, migrations []Migration) ([]Migration, error) {
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

		if err := apply(ctx, db, migration, migration.Up, true); err != nil {
			return ran, err
		}

		ran = append(ran, migration)
	}

	return ran, nil
}

// Down rolls back the most recent migration.
//
// One at a time, on purpose: an accidental "roll everything back" against
// production is a data loss incident, and the extra keystrokes are the cheapest
// safety measure available.
func Down(ctx context.Context, db *sql.DB, migrations []Migration) (Migration, error) {
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return Migration{}, err
	}

	for i := len(migrations) - 1; i >= 0; i-- {
		migration := migrations[i]

		if !applied[migration.Version] {
			continue
		}

		if err := apply(ctx, db, migration, migration.Down, false); err != nil {
			return Migration{}, err
		}

		return migration, nil
	}

	return Migration{}, errors.New("nothing to roll back")
}

func apply(ctx context.Context, db *sql.DB, migration Migration, script string, up bool) error {
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

	for _, statement := range splitStatements(script) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}

	// The bookkeeping row is written in the SAME transaction as the schema
	// change. Two transactions means a crash between them leaves the database
	// changed but not recorded - and the next deploy applies it again.
	if up {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?);`,
			migration.Version, migration.Name)
	} else {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM schema_migrations WHERE version = ?;`, migration.Version)
	}

	if err != nil {
		return fmt.Errorf("record migration %d: %w", migration.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}

	committed = true

	return nil
}

// Report returns the applied state of every migration.
func Report(ctx context.Context, db *sql.DB, migrations []Migration) ([]Status, error) {
	if _, err := db.ExecContext(ctx, schemaTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations;`)
	if err != nil {
		return nil, fmt.Errorf("select schema_migrations: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	timestamps := make(map[int]string)

	for rows.Next() {
		var (
			version   int
			appliedAt string
		)

		if err := rows.Scan(&version, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}

		timestamps[version] = appliedAt
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}

	statuses := make([]Status, 0, len(migrations))

	for _, migration := range migrations {
		appliedAt, applied := timestamps[migration.Version]

		statuses = append(statuses, Status{
			Version:   migration.Version,
			Name:      migration.Name,
			Applied:   applied,
			AppliedAt: appliedAt,
		})
	}

	return statuses, nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	if _, err := db.ExecContext(ctx, schemaTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations;`)
	if err != nil {
		return nil, fmt.Errorf("select applied versions: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

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
