package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-67/internal/store"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-67/internal/testenv"
)

/*
One suite, two possible backends.

Nothing below mentions Docker, SQLite or Postgres except where the engines
genuinely differ. That is what the helper buys: the same assertions run on a
laptop without Docker and on a CI runner with it, and the second one catches
the SQL that only Postgres rejects.

	go test ./...                       # whatever the machine can offer
	TESTCONTAINERS=off go test ./...    # force the SQLite path
	TESTCONTAINERS=on  go test ./...    # require Postgres; no silent skips
*/

func TestMain(m *testing.M) {
	code := m.Run()

	// The shared container is stopped once, after every test in this binary.
	testenv.StopContainer()

	os.Exit(code)
}

func TestCreateAndRead(t *testing.T) {
	t.Parallel()

	accounts, backend := testenv.New(t)

	t.Logf("running against %s", backend)

	created, err := accounts.Create(t.Context(), " ADA@Example.com ", "pro")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.ID == 0 {
		t.Fatal("no id assigned")
	}

	if created.Email != "ada@example.com" {
		t.Fatalf("email = %q, want it normalised", created.Email)
	}

	if created.CreatedAt.IsZero() {
		t.Fatal("created_at was not set")
	}

	found, err := accounts.ByEmail(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatalf("by email: %v", err)
	}

	if found.ID != created.ID || found.Plan != "pro" {
		t.Fatalf("found = %+v", found)
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()

	accounts, _ := testenv.New(t)

	if _, err := accounts.ByID(t.Context(), 999_999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	if _, err := accounts.ByEmail(t.Context(), "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestUniqueConstraint is the case that justifies a real database: no fake
// enforces a unique index.
func TestUniqueConstraint(t *testing.T) {
	t.Parallel()

	accounts, _ := testenv.New(t)

	if _, err := accounts.Create(t.Context(), "dup@example.com", "free"); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := accounts.Create(t.Context(), "DUP@example.com", "free")

	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

// TestParallelTestsAreIsolated: several tests run at once, each creating the
// same email. They can only all pass if each has its own database (SQLite) or
// its own schema (Postgres).
func TestParallelTestsAreIsolated(t *testing.T) {
	t.Parallel()

	for i := range 4 {
		t.Run(fmt.Sprintf("worker-%d", i), func(t *testing.T) {
			t.Parallel()

			accounts, _ := testenv.New(t)

			if _, err := accounts.Create(t.Context(), "same@example.com", "free"); err != nil {
				t.Fatalf("create: %v", err)
			}

			count, err := accounts.Count(t.Context())
			if err != nil {
				t.Fatalf("count: %v", err)
			}

			if count != 1 {
				t.Fatalf("count = %d, want 1 - the tests are sharing a database", count)
			}
		})
	}
}

func TestTruncateResetsState(t *testing.T) {
	t.Parallel()

	accounts, _ := testenv.New(t)
	ctx := context.Background()

	for i := range 3 {
		if _, err := accounts.Create(ctx, fmt.Sprintf("user%d@example.com", i), "free"); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	if err := accounts.Truncate(ctx); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	count, err := accounts.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 0 {
		t.Fatalf("count after truncate = %d", count)
	}
}

// TestPostgresSpecificBehaviour is skipped without Docker, and says why. A
// skipped test that explains itself is honest; one that silently passes is not.
func TestPostgresSpecificBehaviour(t *testing.T) {
	t.Parallel()

	accounts, backend := testenv.New(t)

	testenv.RequirePostgres(t, backend)

	ctx := context.Background()

	if _, err := accounts.Create(ctx, "tx@example.com", "pro"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Timestamps come back as TIMESTAMPTZ, with a real zone - the kind of
	// detail SQLite cannot verify.
	account, err := accounts.ByEmail(ctx, "tx@example.com")
	if err != nil {
		t.Fatalf("by email: %v", err)
	}

	if account.CreatedAt.Location() == nil {
		t.Fatal("timestamp arrived without a location")
	}
}

// TestConcurrentInsertsRaceOnTheIndex needs the engine's real locking, so it
// runs on both backends but means the most on Postgres.
func TestConcurrentInsertsRaceOnTheIndex(t *testing.T) {
	t.Parallel()

	accounts, _ := testenv.New(t)

	const writers = 8

	var (
		waitGroup  sync.WaitGroup
		mu         sync.Mutex
		created    int
		duplicates int
	)

	for range writers {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			_, err := accounts.Create(context.Background(), "race@example.com", "free")

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				created++
			case errors.Is(err, store.ErrDuplicate):
				duplicates++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	if created != 1 {
		t.Fatalf("%d writers created a row, want exactly 1", created)
	}

	if duplicates != writers-1 {
		t.Fatalf("duplicates = %d, want %d", duplicates, writers-1)
	}
}
