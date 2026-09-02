package idempotency_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/idempotency"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)", t.Name()))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	db.SetMaxOpenConns(1)

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	statements := []string{
		idempotency.Schema,
		`CREATE TABLE IF NOT EXISTS side_effects (id INTEGER PRIMARY KEY AUTOINCREMENT, note TEXT NOT NULL);`,
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	return db
}

func sideEffects(t *testing.T, db *sql.DB) int {
	t.Helper()

	var count int

	if err := db.QueryRow(`SELECT COUNT(*) FROM side_effects;`).Scan(&count); err != nil {
		t.Fatalf("count side effects: %v", err)
	}

	return count
}

func record(note string) idempotency.Handler {
	return func(ctx context.Context, tx *sql.Tx) (string, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO side_effects (note) VALUES (?);`, note); err != nil {
			return "", err
		}

		return "ok", nil
	}
}

func TestDuplicateDeliveryRunsTheHandlerOnce(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	if err := store.Process(t.Context(), "mailer", "evt-1", record("first")); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	err := store.Process(t.Context(), "mailer", "evt-1", record("second"))
	if !errors.Is(err, idempotency.ErrAlreadyProcessed) {
		t.Fatalf("second delivery error = %v, want ErrAlreadyProcessed", err)
	}

	if got := sideEffects(t, db); got != 1 {
		t.Errorf("side effects = %d, want 1", got)
	}
}

// The claim is scoped per consumer: two services both care about one event.
func TestEachConsumerProcessesTheEventOnce(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	for _, consumer := range []string{"mailer", "analytics"} {
		if err := store.Process(t.Context(), consumer, "evt-1", record(consumer)); err != nil {
			t.Fatalf("%s: %v", consumer, err)
		}

		err := store.Process(t.Context(), consumer, "evt-1", record(consumer))
		if !errors.Is(err, idempotency.ErrAlreadyProcessed) {
			t.Fatalf("%s redelivery = %v, want ErrAlreadyProcessed", consumer, err)
		}
	}

	if got := sideEffects(t, db); got != 2 {
		t.Errorf("side effects = %d, want 2 (one per consumer)", got)
	}
}

// A failing handler must not leave a claim behind, or the retry would be
// swallowed as a duplicate and the work would never happen.
func TestFailedHandlerReleasesTheClaim(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	boom := errors.New("downstream is down")

	err := store.Process(t.Context(), "mailer", "evt-1", func(context.Context, *sql.Tx) (string, error) {
		return "", boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the handler's error", err)
	}

	processed, err := store.WasProcessed(t.Context(), "mailer", "evt-1")
	if err != nil {
		t.Fatalf("was processed: %v", err)
	}

	if processed {
		t.Fatal("a failed handler left its claim behind; the retry would be skipped")
	}

	if err := store.Process(t.Context(), "mailer", "evt-1", record("retry")); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if got := sideEffects(t, db); got != 1 {
		t.Errorf("side effects = %d, want 1", got)
	}
}

// The handler's writes and the claim commit together; a rollback takes both.
func TestHandlerSideEffectsRollBackWithTheClaim(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	err := store.Process(t.Context(), "mailer", "evt-1", func(ctx context.Context, tx *sql.Tx) (string, error) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO side_effects (note) VALUES ('half done');`); err != nil {
			return "", err
		}

		return "", errors.New("failed after writing")
	})
	if err == nil {
		t.Fatal("expected the handler error")
	}

	if got := sideEffects(t, db); got != 0 {
		t.Errorf("side effects = %d, want 0 (the write must roll back with the claim)", got)
	}
}

// Concurrent deliveries of the same event: exactly one wins.
func TestConcurrentDeliveriesProcessOnce(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	const deliveries = 8

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		duplicate int
	)

	for i := 0; i < deliveries; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			err := store.Process(t.Context(), "mailer", "evt-1", record(fmt.Sprint(i)))

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, idempotency.ErrAlreadyProcessed):
				duplicate++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}

	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1", succeeded)
	}

	if duplicate != deliveries-1 {
		t.Errorf("duplicates = %d, want %d", duplicate, deliveries-1)
	}

	if got := sideEffects(t, db); got != 1 {
		t.Errorf("side effects = %d, want 1", got)
	}
}

func TestPruneKeepsRecentClaims(t *testing.T) {
	db := newDB(t)
	store := idempotency.NewStore(db)

	if err := store.Process(t.Context(), "mailer", "evt-1", record("first")); err != nil {
		t.Fatalf("process: %v", err)
	}

	// Nothing is old enough yet.
	removed, err := store.Prune(t.Context(), time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if removed != 0 {
		t.Errorf("pruned %d rows, want 0", removed)
	}

	// Age the row past the retention window.
	if _, err := db.Exec(`UPDATE processed_events SET processed_at = datetime('now', '-2 hours');`); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	if removed, err = store.Prune(t.Context(), time.Hour); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if removed != 1 {
		t.Errorf("pruned %d rows, want 1", removed)
	}

	count, err := store.Count(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}
