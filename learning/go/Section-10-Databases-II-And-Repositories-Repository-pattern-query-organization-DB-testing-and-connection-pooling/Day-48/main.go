package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
Day 48 - Databases (II) & Repositories: Testing with Databases

Tasks covered:

 1. Tests run against a disposable database, never a shared real one
 2. State is reset between tests - three strategies, measured below
 3. Repository integration tests cover create, read and not-found paths
 4. Parallel safety: which strategies may use t.Parallel and which may not

Files:

	store.go        the repository under test (takes a DBTX: *sql.DB or *sql.Tx)
	testdb_test.go  the three isolation helpers, with their Postgres equivalents
	store_test.go   the actual integration tests

Run:

	go run .            # measure the cost of each reset strategy
	CASES=200 go run .

Test:

	go test ./...
	go test -race -v ./...

Environment variables:

	CASES   simulated test cases per strategy. Default: 100

The point of this program: the reset strategy is a real cost decision, and it
is also a correctness decision. The last section shows what a shared database
without a reset actually does to a test suite.
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day48: ")

	ctx := context.Background()
	cases := envInt("CASES", 100)

	fmt.Printf("\nSimulating %d test cases per strategy\n", cases)
	fmt.Println(strings.Repeat("-", 74))
	fmt.Printf("%-34s %-12s %-10s %s\n", "STRATEGY", "TOTAL", "PER CASE", "t.Parallel safe?")

	strategies := []struct {
		name     string
		run      func(context.Context, int) error
		parallel string
	}{
		{"fresh database per case", runFreshDatabase, "yes"},
		{"shared database + transaction", runTransactionRollback, "yes"},
		{"shared database + truncate", runTruncate, "no"},
	}

	for _, strategy := range strategies {
		start := time.Now()

		if err := strategy.run(ctx, cases); err != nil {
			log.Fatalf("%s: %v", strategy.name, err)
		}

		elapsed := time.Since(start)

		fmt.Printf("%-34s %-12s %-10s %s\n",
			strategy.name,
			elapsed.Round(time.Millisecond),
			(elapsed / time.Duration(cases)).Round(time.Microsecond),
			strategy.parallel,
		)
	}

	fmt.Println("\nA fresh database is the safest and the slowest; a rolled back")
	fmt.Println("transaction is nearly free but forces every test to use the *sql.Tx;")
	fmt.Println("truncate is cheap but serialises the suite.")

	if err := demonstrateDirtyState(ctx); err != nil {
		log.Fatalf("dirty state demo: %v", err)
	}

	if err := demonstrateParallelIsolation(ctx); err != nil {
		log.Fatalf("parallel demo: %v", err)
	}
}

// runFreshDatabase: strategy 1. Every case pays for schema creation.
func runFreshDatabase(ctx context.Context, cases int) error {
	directory, err := os.MkdirTemp("", "day48-fresh-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			log.Printf("remove temp dir: %v", err)
		}
	}()

	for i := range cases {
		db, err := OpenDB(ctx, filepath.Join(directory, "case-"+strconv.Itoa(i)+".db"))
		if err != nil {
			return err
		}

		if err := exerciseStore(ctx, NewStore(db)); err != nil {
			return errors.Join(err, db.Close())
		}

		if err := db.Close(); err != nil {
			return fmt.Errorf("close case database: %w", err)
		}
	}

	return nil
}

// runTransactionRollback: strategy 2. The schema is created once and nothing
// is ever written permanently.
func runTransactionRollback(ctx context.Context, cases int) error {
	db, err := OpenDB(ctx, ":memory:")
	if err != nil {
		return err
	}

	defer closeQuietly(db)

	for range cases {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin: %w", err)
		}

		if err := exerciseStore(ctx, NewStore(tx)); err != nil {
			return errors.Join(err, tx.Rollback())
		}

		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return fmt.Errorf("rollback: %w", err)
		}
	}

	return nil
}

// runTruncate: strategy 3. Cheap, but every case mutates shared state.
func runTruncate(ctx context.Context, cases int) error {
	db, err := OpenDB(ctx, ":memory:")
	if err != nil {
		return err
	}

	defer closeQuietly(db)

	store := NewStore(db)

	for range cases {
		if err := Truncate(ctx, db); err != nil {
			return err
		}

		if err := exerciseStore(ctx, store); err != nil {
			return err
		}
	}

	return nil
}

// exerciseStore is a stand-in for one test case: a write, a read, a
// constraint check and a not-found path.
func exerciseStore(ctx context.Context, store *Store) error {
	account, err := store.CreateAccount(ctx, "case@example.com", "pro")
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}

	if _, err := store.AccountByID(ctx, account.ID); err != nil {
		return fmt.Errorf("read account: %w", err)
	}

	invoice, err := store.CreateInvoice(ctx, account.ID, 4999)
	if err != nil {
		return fmt.Errorf("create invoice: %w", err)
	}

	if err := store.MarkInvoicePaid(ctx, invoice.ID); err != nil {
		return fmt.Errorf("mark paid: %w", err)
	}

	if _, err := store.AccountByID(ctx, 999_999); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("expected ErrNotFound, got %v", err)
	}

	return nil
}

// demonstrateDirtyState shows the failure mode the strategies above exist to
// prevent: without a reset, the second case fails on rows the first left.
func demonstrateDirtyState(ctx context.Context) error {
	fmt.Println("\nWhat happens without a reset")
	fmt.Println(strings.Repeat("-", 74))

	db, err := OpenDB(ctx, ":memory:")
	if err != nil {
		return err
	}

	defer closeQuietly(db)

	store := NewStore(db)

	for i := range 2 {
		err := exerciseStore(ctx, store)

		switch {
		case err == nil:
			fmt.Printf("  case %d: passed\n", i+1)

		case errors.Is(err, ErrDuplicate):
			fmt.Printf("  case %d: FAILED - %v\n", i+1, err)
			fmt.Println("  the row from case 1 is still there; the test suite now depends on order")

		default:
			return err
		}
	}

	return nil
}

// demonstrateParallelIsolation runs the same case concurrently against
// separate databases. This is why strategy 1 can use t.Parallel: there is no
// shared mutable state to collide over.
func demonstrateParallelIsolation(ctx context.Context) error {
	fmt.Println("\nParallel cases, one database each")
	fmt.Println(strings.Repeat("-", 74))

	directory, err := os.MkdirTemp("", "day48-parallel-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			log.Printf("remove temp dir: %v", err)
		}
	}()

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		failures  []error
	)

	const workers = 8

	start := time.Now()

	for i := range workers {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			db, err := OpenDB(ctx, filepath.Join(directory, fmt.Sprintf("worker-%d.db", i)))
			if err != nil {
				record(&mu, &failures, err)
				return
			}

			defer closeQuietly(db)

			// Every worker writes the same email. On a shared database this
			// would be a unique-constraint failure; here each has its own.
			if err := exerciseStore(ctx, NewStore(db)); err != nil {
				record(&mu, &failures, err)
			}
		}(i)
	}

	waitGroup.Wait()

	if len(failures) > 0 {
		return fmt.Errorf("%d parallel case(s) failed, first: %w", len(failures), failures[0])
	}

	fmt.Printf("  %d parallel cases, identical data, zero collisions, %s\n",
		workers, time.Since(start).Round(time.Millisecond))
	fmt.Println("  the same run against one shared database would fail on the unique index")

	return nil
}

func record(mu *sync.Mutex, failures *[]error, err error) {
	mu.Lock()
	defer mu.Unlock()

	*failures = append(*failures, err)
}

func closeQuietly(db *sql.DB) {
	if err := db.Close(); err != nil {
		log.Printf("close database: %v", err)
	}
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
