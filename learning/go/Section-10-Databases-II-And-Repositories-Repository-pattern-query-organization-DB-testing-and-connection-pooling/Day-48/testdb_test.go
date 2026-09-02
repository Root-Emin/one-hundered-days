package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

/*
Test database helpers.

Three isolation strategies, in the order you should reach for them:

 1. newIsolatedDB   - a disposable database per test. Total isolation, safe to
    run in parallel, costs a schema creation per test.
 2. withRollback    - one shared database, each test inside a transaction that
    is always rolled back. Fastest reset; the test must use the *sql.Tx.
 3. sharedDB + reset - one database truncated between tests. Cheap, but only
    safe for tests that do NOT run in parallel.

Postgres equivalents:
 1. CREATE DATABASE test_<random> / a fresh schema per package
 2. identical: BEGIN ... ROLLBACK
 3. TRUNCATE invoices, accounts RESTART IDENTITY CASCADE

Never point tests at a database that also holds data anyone cares about. The
reset strategies here delete rows by design.
*/

// newIsolatedDB gives this test its own database file in its own temp
// directory, removed by the testing package when the test ends.
func newIsolatedDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := OpenDB(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open isolated test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return db
}

// withRollback runs the test body inside a transaction that is always rolled
// back, so the database is unchanged whether the test passes or fails.
func withRollback(t *testing.T, db *sql.DB, body func(store *Store)) {
	t.Helper()

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}

	// Cleanup, not defer: it runs even if the body calls t.Fatal, which
	// exits the goroutine before any defer in this function would fire.
	t.Cleanup(func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("rollback test transaction: %v", err)
		}
	})

	body(NewStore(tx))
}

// sharedTestDB is the package-level database used by the truncate strategy.
// It is created once, on first use.
var (
	sharedOnce sync.Once
	sharedDB   *sql.DB
	sharedErr  error
)

func openSharedDB(t *testing.T) *sql.DB {
	t.Helper()

	sharedOnce.Do(func() {
		// TestMain owns the lifetime of this handle, so it is not registered
		// with t.Cleanup here.
		sharedDB, sharedErr = OpenDB(context.Background(), ":memory:")
	})

	if sharedErr != nil {
		t.Fatalf("open shared test database: %v", sharedErr)
	}

	return sharedDB
}

// resetShared empties the shared database. Tests using it must not call
// t.Parallel: they would truncate each other's rows mid-assertion.
func resetShared(t *testing.T, db *sql.DB) {
	t.Helper()

	if err := Truncate(t.Context(), db); err != nil {
		t.Fatalf("truncate shared database: %v", err)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()

	if sharedDB != nil {
		if err := sharedDB.Close(); err != nil {
			panic("close shared test database: " + err.Error())
		}
	}

	// os.Exit skips deferred functions, which is why the close above is
	// explicit and comes first.
	exit(code)
}
