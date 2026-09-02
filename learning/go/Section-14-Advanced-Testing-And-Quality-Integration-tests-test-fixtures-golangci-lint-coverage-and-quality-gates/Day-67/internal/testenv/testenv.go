// Package testenv is the single place that knows how to get a database for a
// test.
//
// Two backends, one API:
//
//	Docker available  -> one Postgres container per test run, a fresh schema
//	                     per test, dropped afterwards
//	Docker absent     -> a throwaway SQLite file per test
//
// Centralising this is the point of today: without it, every test file grows
// its own copy of the setup, the copies drift, and half of them forget to tear
// down. With it, a test says New(t) and gets a clean database, whatever the
// machine can offer.
package testenv

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-67/internal/store"
)

// Backend reports which engine a test ended up running against, so a test can
// skip a case that only makes sense on one of them.
type Backend string

const (
	BackendSQLite   Backend = "sqlite"
	BackendPostgres Backend = "postgres"
)

var (
	// The container is started once per test binary and shared. Starting one
	// per test would add seconds per case; sharing it is safe because each
	// test gets its own schema.
	containerOnce sync.Once
	containerDSN  string
	containerErr  error
	containerStop func()

	schemaCounter atomic.Int64
)

// Available reports whether the Postgres path can be used at all.
//
//	TESTCONTAINERS=off  forces the SQLite path even when Docker is running
//	TESTCONTAINERS=on   forces the Postgres path; failure to start is a
//	                    test failure instead of a skip (what CI wants)
func Available() bool {
	switch strings.ToLower(os.Getenv("TESTCONTAINERS")) {
	case "off", "0", "false":
		return false
	case "on", "1", "true":
		return true
	}

	return dockerReachable()
}

// Required reports whether a missing Docker should fail rather than skip.
func Required() bool {
	switch strings.ToLower(os.Getenv("TESTCONTAINERS")) {
	case "on", "1", "true":
		return true
	default:
		return false
	}
}

// New returns a migrated store plus the backend it is running on.
//
// Every resource it creates is released through t.Cleanup, which runs even
// when the test fails - the property that keeps CI runners from filling up
// with abandoned containers and schemas.
func New(t *testing.T) (*store.Store, Backend) {
	t.Helper()

	if Available() {
		bookmarks, err := newPostgres(t)

		switch {
		case err == nil:
			return bookmarks, BackendPostgres

		case Required():
			t.Fatalf("TESTCONTAINERS is required but Postgres could not start: %v", err)

		default:
			t.Logf("Postgres unavailable (%v); falling back to SQLite", err)
		}
	}

	return newSQLite(t), BackendSQLite
}

// RequirePostgres skips a test that is meaningless without the real engine.
//
// Skipping loudly beats a test that silently passes against the wrong
// backend: the log line tells the developer why coverage is thinner today.
func RequirePostgres(t *testing.T, backend Backend) {
	t.Helper()

	if backend != BackendPostgres {
		t.Skip("needs Postgres: start Docker, or run with TESTCONTAINERS=on to make this a failure")
	}
}

func newSQLite(t *testing.T) *store.Store {
	t.Helper()

	// A file per test: total isolation, safe under t.Parallel.
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})

	bookmarks := store.New(db, store.SQLite, "")

	if err := bookmarks.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	return bookmarks
}

func newPostgres(t *testing.T) (*store.Store, error) {
	t.Helper()

	dsn, err := containerDataSource()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("close postgres: %v", closeErr)
		}

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// A schema per test is the Postgres answer to "isolate parallel tests":
	// same container, same connection settings, no shared rows, no port
	// collisions.
	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano()%1_000_000, schemaCounter.Add(1))

	accounts := store.New(db, store.Postgres, schema)

	if err := accounts.Migrate(context.Background()); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("close postgres: %v", closeErr)
		}

		return nil, fmt.Errorf("migrate postgres: %w", err)
	}

	t.Cleanup(func() {
		// Drop the schema first, then the handle. Doing it in a cleanup means
		// a panicking test still releases the resources.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()

		if err := accounts.DropSchema(dropCtx); err != nil {
			t.Errorf("drop schema: %v", err)
		}

		if err := db.Close(); err != nil {
			t.Errorf("close postgres: %v", err)
		}
	})

	return accounts, nil
}

// StopContainer terminates the shared container. A TestMain should call it, so
// the container dies with the test binary rather than lingering.
func StopContainer() {
	if containerStop != nil {
		containerStop()
	}
}
