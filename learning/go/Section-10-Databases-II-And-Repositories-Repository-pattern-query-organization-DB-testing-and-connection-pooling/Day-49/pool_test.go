package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func testPoolConfig(maxOpen int) PoolConfig {
	return PoolConfig{
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxOpen,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: 30 * time.Second,
	}
}

func openTestPool(t *testing.T, maxOpen int) *sql.DB {
	t.Helper()

	db, err := OpenPool(t.Context(), ":memory:", testPoolConfig(maxOpen))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	t.Cleanup(func() {
		// Closing twice is fine for a pool that a test already closed on
		// purpose; database/sql makes Close idempotent.
		if err := db.Close(); err != nil {
			t.Errorf("close pool: %v", err)
		}
	})

	return db
}

func TestPoolConfigIsApplied(t *testing.T) {
	db := openTestPool(t, 3)

	if got := db.Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("MaxOpenConnections = %d, want 3", got)
	}
}

// TestSaturationIsObserved is the regression test for the monitoring code: a
// pool of one under concurrent load must report waits.
func TestSaturationIsObserved(t *testing.T) {
	db := openTestPool(t, 1)
	monitor := NewPoolMonitor(db, 2*time.Millisecond)

	ctx, stop := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		defer close(done)

		monitor.Run(ctx)
	}()

	var waitGroup sync.WaitGroup

	for range 8 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			var count int64

			if err := db.QueryRowContext(context.Background(), slowQuery).Scan(&count); err != nil {
				t.Errorf("query: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	stop()
	<-done

	if got := db.Stats().WaitCount; got == 0 {
		t.Fatal("WaitCount = 0: eight callers shared one connection without ever queueing")
	}

	if monitor.SaturatedSamples() == 0 {
		t.Fatal("monitor never reported saturation while the pool was fully checked out")
	}
}

// TestLargePoolDoesNotQueue is the other half: with enough connections, no
// caller ever waits.
func TestLargePoolDoesNotQueue(t *testing.T) {
	db := openTestPool(t, 8)

	var waitGroup sync.WaitGroup

	for range 8 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			var count int64

			if err := db.QueryRowContext(context.Background(), slowQuery).Scan(&count); err != nil {
				t.Errorf("query: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	if got := db.Stats().WaitCount; got != 0 {
		t.Fatalf("WaitCount = %d, want 0 with one connection per caller", got)
	}
}

// TestPoolWaitRespectsContext proves why an unbounded pool wait is dangerous:
// the caller's deadline covers the queueing time, so exhaustion surfaces as a
// request timeout rather than a hang.
func TestPoolWaitRespectsContext(t *testing.T) {
	db := openTestPool(t, 1)

	// Hold the only connection.
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("take connection: %v", err)
	}

	defer func() {
		if err := held.Close(); err != nil {
			t.Errorf("release connection: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var value int

	err = db.QueryRowContext(ctx, `SELECT 1;`).Scan(&value)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestClosePoolStopsFurtherQueries(t *testing.T) {
	db, err := OpenPool(t.Context(), ":memory:", testPoolConfig(2))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	if err := ClosePool(db); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	var value int

	// database/sql reports this as an unexported sentinel ("sql: database is
	// closed"), so the assertion is on the behaviour: no query gets through.
	err = db.QueryRowContext(context.Background(), `SELECT 1;`).Scan(&value)

	if err == nil {
		t.Fatal("query succeeded after the pool was closed")
	}

	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("err = %v, want a closed-database error", err)
	}
}

func TestDefaultPoolConfigIsBounded(t *testing.T) {
	config := DefaultPoolConfig()

	if config.MaxOpenConns <= 0 {
		t.Fatal("MaxOpenConns must be bounded: an unlimited pool can exhaust the database")
	}

	if config.MaxIdleConns > config.MaxOpenConns {
		t.Fatalf("MaxIdleConns (%d) > MaxOpenConns (%d): idle connections would be closed immediately",
			config.MaxIdleConns, config.MaxOpenConns)
	}

	if config.ConnMaxLifetime <= 0 {
		t.Fatal("ConnMaxLifetime must be set so connections rotate across failovers")
	}
}
