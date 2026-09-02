// Package testsupport holds the shared test helpers: database setup, fixtures
// and teardown.
//
// It is a normal package rather than a _test.go file because more than one
// package's tests need it. Everything here takes a *testing.T and registers
// its own cleanup, so a caller cannot forget to tear down.
package testsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/store"
)

// NewDatabase returns a disposable database for one test.
//
// t.TempDir() is removed by the testing package when the test ends - including
// when it fails, which is the property that keeps CI runners clean.
func NewDatabase(t *testing.T) *sql.DB {
	t.Helper()

	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return db
}

func NewStore(t *testing.T) *store.Store {
	t.Helper()

	return store.New(NewDatabase(t))
}

// Fixture describes the state a test needs. Declaring it as data rather than
// as a sequence of calls keeps the test readable and the setup reusable.
type Fixture struct {
	Owner string
	URL   string
	Title string
	Tags  []string
}

// DefaultFixtures is the minimal known state most cases want: two owners, so
// every test can check that one cannot see the other's rows.
func DefaultFixtures() []Fixture {
	return []Fixture{
		{Owner: "ada", URL: "https://go.dev", Title: "The Go website", Tags: []string{"go", "docs"}},
		{Owner: "ada", URL: "https://pkg.go.dev", Title: "Package index", Tags: []string{"go"}},
		{Owner: "alan", URL: "https://example.com", Title: "Someone else's bookmark", Tags: []string{"misc"}},
	}
}

// Seed inserts fixtures and returns the created rows.
//
// It registers a cleanup that empties the tables afterwards. With a per-test
// database that is redundant; with a shared one it is what keeps cases
// independent, and writing it once means no test has to remember.
func Seed(t *testing.T, bookmarks *store.Store, fixtures ...Fixture) []store.Bookmark {
	t.Helper()

	if len(fixtures) == 0 {
		fixtures = DefaultFixtures()
	}

	ctx := context.Background()

	created := make([]store.Bookmark, 0, len(fixtures))

	for _, fixture := range fixtures {
		bookmark, err := bookmarks.Create(ctx, store.Bookmark{
			Owner: fixture.Owner,
			URL:   fixture.URL,
			Title: fixture.Title,
			Tags:  fixture.Tags,
		})
		if err != nil {
			t.Fatalf("seed %s: %v", fixture.URL, err)
		}

		created = append(created, bookmark)
	}

	t.Cleanup(func() {
		// Teardown runs even when the test fails or panics.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := bookmarks.Truncate(cleanupCtx); err != nil {
			t.Errorf("truncate after test: %v", err)
		}
	})

	return created
}
