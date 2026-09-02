package notes_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/assets"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/migrate"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/notes"
)

// newStore migrates a fresh database, so the tests exercise the same schema
// the service runs against - not a hand-written CREATE TABLE that drifts.
func newStore(t *testing.T) *notes.Store {
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

	migrations, err := migrate.Load(assets.Migrations, assets.MigrationsDir)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}

	if _, err := migrate.Up(t.Context(), db, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return notes.New(db)
}

func TestCreateAndGet(t *testing.T) {
	store := newStore(t)

	created, err := store.Create(t.Context(), "first", "hello")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Errorf("created = %+v", created)
	}

	fetched, err := store.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if fetched.Title != "first" || fetched.Body != "hello" {
		t.Errorf("fetched = %+v", fetched)
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	if _, err := newStore(t).Get(t.Context(), 999); !errors.Is(err, notes.ErrNotFound) {
		t.Errorf("Get(999) = %v, want ErrNotFound", err)
	}
}

func TestCreateRequiresATitle(t *testing.T) {
	if _, err := newStore(t).Create(t.Context(), "", "body"); err == nil {
		t.Error("expected an error for an empty title")
	}
}

func TestListIsNewestFirst(t *testing.T) {
	store := newStore(t)

	for _, title := range []string{"one", "two", "three"} {
		if _, err := store.Create(t.Context(), title, ""); err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
	}

	list, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("list = %d, want 3", len(list))
	}

	if list[0].Title != "three" {
		t.Errorf("first = %q, want three (newest first)", list[0].Title)
	}

	count, err := store.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}

	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}
