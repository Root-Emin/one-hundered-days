package migrate_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/assets"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/migrate"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite",
		"file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return db
}

func load(t *testing.T) []migrate.Migration {
	t.Helper()

	migrations, err := migrate.Load(assets.Migrations, assets.MigrationsDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return migrations
}

func TestLoadReadsOrderedPairs(t *testing.T) {
	migrations := load(t)

	if len(migrations) < 2 {
		t.Fatalf("migrations = %d, want at least 2", len(migrations))
	}

	for i, migration := range migrations {
		if migration.Up == "" || migration.Down == "" {
			t.Errorf("migration %d has an empty script", migration.Version)
		}

		if i > 0 && migrations[i-1].Version >= migration.Version {
			t.Errorf("migrations are out of order: %d before %d", migrations[i-1].Version, migration.Version)
		}
	}
}

func TestUpIsIdempotent(t *testing.T) {
	db := newDB(t)
	migrations := load(t)

	applied, err := migrate.Up(t.Context(), db, migrations)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	if len(applied) != len(migrations) {
		t.Fatalf("applied %d, want %d", len(applied), len(migrations))
	}

	// Running it again must do nothing - which is what makes "migrate on
	// startup" safe.
	applied, err = migrate.Up(t.Context(), db, migrations)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}

	if len(applied) != 0 {
		t.Errorf("second Up applied %d migrations, want 0", len(applied))
	}
}

func TestUpCreatesTheSchema(t *testing.T) {
	db := newDB(t)

	if _, err := migrate.Up(t.Context(), db, load(t)); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO notes (title, body) VALUES ('t', 'b');`); err != nil {
		t.Errorf("the notes table is not usable after migrating: %v", err)
	}
}

func TestDownRollsBackOneAtATime(t *testing.T) {
	db := newDB(t)
	migrations := load(t)

	if _, err := migrate.Up(t.Context(), db, migrations); err != nil {
		t.Fatalf("Up: %v", err)
	}

	rolled, err := migrate.Down(t.Context(), db, migrations)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}

	if rolled.Version != migrations[len(migrations)-1].Version {
		t.Errorf("rolled back %d, want the newest (%d)", rolled.Version, migrations[len(migrations)-1].Version)
	}

	statuses, err := migrate.Report(t.Context(), db, migrations)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if statuses[len(statuses)-1].Applied {
		t.Error("the rolled-back migration is still recorded as applied")
	}

	if !statuses[0].Applied {
		t.Error("Down rolled back more than one migration")
	}
}

func TestDownOnAnEmptyDatabaseIsAnError(t *testing.T) {
	if _, err := migrate.Down(t.Context(), newDB(t), load(t)); err == nil {
		t.Error("expected an error when there is nothing to roll back")
	}
}

func TestReportShowsPendingAndApplied(t *testing.T) {
	db := newDB(t)
	migrations := load(t)

	statuses, err := migrate.Report(t.Context(), db, migrations)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	for _, status := range statuses {
		if status.Applied {
			t.Errorf("migration %d reported as applied on an empty database", status.Version)
		}
	}

	if _, err := migrate.Up(t.Context(), db, migrations); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if statuses, err = migrate.Report(t.Context(), db, migrations); err != nil {
		t.Fatalf("Report: %v", err)
	}

	for _, status := range statuses {
		if !status.Applied || status.AppliedAt == "" {
			t.Errorf("migration %d = %+v, want applied with a timestamp", status.Version, status)
		}
	}
}

// A migration with no down script is a deploy with no way back, so Load
// refuses it rather than warning.
//
// Load takes an fs.FS, so the fixture is an in-memory filesystem - no files on
// disk, no testdata directory to keep in step with the test.
func TestLoadRejectsAMigrationWithNoDownScript(t *testing.T) {
	files := fstest.MapFS{
		"migrations/0001_only_up.up.sql": &fstest.MapFile{Data: []byte("CREATE TABLE t (id INTEGER);")},
	}

	if _, err := migrate.Load(files, "migrations"); err == nil {
		t.Error("expected an error for a migration with no down script")
	}
}

func TestLoadRejectsBadFileNames(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"no version prefix": {
			"migrations/no_version.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		},
		"version is not a number": {
			"migrations/abc_thing.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		},
		"neither up nor down": {
			"migrations/0001_thing.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		},
	}

	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := migrate.Load(files, "migrations"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// The same runner works against a plain directory, which is what makes it
// usable from a test or a development tool without embedding anything.
func TestLoadWorksFromADirectory(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, "migrations", name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("0001_create.up.sql", "CREATE TABLE t (id INTEGER);")
	write("0001_create.down.sql", "DROP TABLE t;")

	migrations, err := migrate.Load(os.DirFS(dir), "migrations")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(migrations) != 1 || migrations[0].Name != "create" {
		t.Errorf("migrations = %+v", migrations)
	}
}

// A failing migration must leave the database at a known version: the schema
// change and its bookkeeping row commit together, or neither does.
func TestFailedMigrationRecordsNothing(t *testing.T) {
	db := newDB(t)

	broken := []migrate.Migration{{
		Version: 1,
		Name:    "broken",
		Up:      "CREATE TABLE good (id INTEGER); INSERT INTO nonexistent VALUES (1);",
		Down:    "DROP TABLE IF EXISTS good;",
	}}

	if _, err := migrate.Up(t.Context(), db, broken); err == nil {
		t.Fatal("expected the broken migration to fail")
	}

	statuses, err := migrate.Report(t.Context(), db, broken)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	if statuses[0].Applied {
		t.Error("a failed migration was recorded as applied")
	}

	var count int

	err = db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='good';`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}

	if count != 0 {
		t.Error("the first statement's table survived; the migration was not transactional")
	}
}
