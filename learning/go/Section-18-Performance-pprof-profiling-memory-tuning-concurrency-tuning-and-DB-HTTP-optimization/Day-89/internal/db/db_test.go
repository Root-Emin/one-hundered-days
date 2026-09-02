package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-89/internal/db"
)

func newStore(t *testing.T) *db.Store {
	t.Helper()

	handle, err := sql.Open("sqlite",
		"file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	store := db.New(handle)

	if err := store.Exec(t.Context(), db.Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	return store
}

func seeded(t *testing.T, customers, ordersPer int) *db.Store {
	t.Helper()

	store := newStore(t)

	if err := db.Seed(t.Context(), store, customers, ordersPer); err != nil {
		t.Fatalf("seed: %v", err)
	}

	return store
}

// The index has to change the PLAN, not just exist. This is the assertion that
// catches a migration adding an index nothing uses.
func TestIndexTurnsAScanIntoASeek(t *testing.T) {
	store := seeded(t, 50, 5)

	before, err := store.Explain(t.Context(), db.OrdersForCustomerSQL, 1, "paid")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if db.Seeks(before) {
		t.Errorf("plan seeks without an index: %s", before)
	}

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	after, err := store.Explain(t.Context(), db.OrdersForCustomerSQL, 1, "paid")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if !db.Seeks(after) {
		t.Errorf("plan still does not seek with the index: %s", after)
	}
}

// An index on (customer_id, status) cannot answer a query on status alone.
func TestIndexIsUsableLeftToRight(t *testing.T) {
	store := seeded(t, 50, 5)

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	plan, err := store.Explain(t.Context(), `SELECT id FROM orders WHERE status = ?;`, "paid")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if db.Seeks(plan) {
		t.Errorf("expected a scan for a query on the second index column: %s", plan)
	}
}

// A function around the leading column throws the index away entirely.
func TestFunctionOnAColumnDisablesTheIndex(t *testing.T) {
	store := seeded(t, 50, 5)

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	plan, err := store.Explain(t.Context(),
		`SELECT id FROM orders WHERE DATE(created_at) = ?;`, "2026-03-01")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if db.Seeks(plan) {
		t.Errorf("expected a scan when the column is wrapped in a function: %s", plan)
	}
}

// The three loading strategies must return the SAME data. An optimization that
// changes the result is not an optimization.
func TestLoadStrategiesAgree(t *testing.T) {
	store := seeded(t, 60, 4)

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	nPlusOne, nQueries, err := store.LoadNPlusOne(t.Context(), "TR", 20)
	if err != nil {
		t.Fatalf("LoadNPlusOne: %v", err)
	}

	joined, joinQueries, err := store.LoadJoined(t.Context(), "TR", 20)
	if err != nil {
		t.Fatalf("LoadJoined: %v", err)
	}

	batched, batchQueries, err := store.LoadBatched(t.Context(), "TR", 20)
	if err != nil {
		t.Fatalf("LoadBatched: %v", err)
	}

	if len(nPlusOne) == 0 {
		t.Fatal("no customers loaded - the fixture is wrong, not the code")
	}

	assertSame(t, "JOIN", nPlusOne, joined)
	assertSame(t, "batched", nPlusOne, batched)

	// And the query counts are the whole point.
	if nQueries != len(nPlusOne)+1 {
		t.Errorf("N+1 issued %d queries for %d customers, want %d", nQueries, len(nPlusOne), len(nPlusOne)+1)
	}

	if joinQueries != 1 {
		t.Errorf("JOIN issued %d queries, want 1", joinQueries)
	}

	if batchQueries != 2 {
		t.Errorf("batched issued %d queries, want 2", batchQueries)
	}
}

func assertSame(t *testing.T, name string, want, got []db.CustomerOrders) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s: %d customers, want %d", name, len(got), len(want))
	}

	for i := range want {
		if got[i].Customer.ID != want[i].Customer.ID {
			t.Fatalf("%s: customer %d = %d, want %d", name, i, got[i].Customer.ID, want[i].Customer.ID)
		}

		if len(got[i].Orders) != len(want[i].Orders) {
			t.Fatalf("%s: customer %d has %d orders, want %d",
				name, got[i].Customer.ID, len(got[i].Orders), len(want[i].Orders))
		}

		for j := range want[i].Orders {
			if got[i].Orders[j].ID != want[i].Orders[j].ID {
				t.Errorf("%s: customer %d order %d = %d, want %d",
					name, got[i].Customer.ID, j, got[i].Orders[j].ID, want[i].Orders[j].ID)
			}
		}
	}
}

// A LEFT JOIN produces NULL columns for a customer with no orders; scanning
// those into plain ints fails, which is the bug this guards.
func TestJoinHandlesCustomersWithNoOrders(t *testing.T) {
	store := newStore(t)

	if _, err := store.DB().ExecContext(t.Context(),
		`INSERT INTO customers (name, country) VALUES ('lonely', 'TR');`); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	result, _, err := store.LoadJoined(t.Context(), "TR", 10)
	if err != nil {
		t.Fatalf("LoadJoined: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("customers = %d, want 1", len(result))
	}

	if len(result[0].Orders) != 0 {
		t.Errorf("orders = %d, want 0", len(result[0].Orders))
	}
}

// The N+1 cost is the query COUNT, which only shows up once a round trip costs
// something. This asserts the mechanism rather than a wall-clock number.
func TestLatencyMakesTheQueryCountVisible(t *testing.T) {
	store := seeded(t, 40, 3)

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	store.SetLatency(2 * time.Millisecond)

	defer store.SetLatency(0)

	start := time.Now()

	_, queries, err := store.LoadNPlusOne(t.Context(), "TR", 10)
	if err != nil {
		t.Fatalf("LoadNPlusOne: %v", err)
	}

	nPlusOneElapsed := time.Since(start)

	start = time.Now()

	if _, _, err = store.LoadJoined(t.Context(), "TR", 10); err != nil {
		t.Fatalf("LoadJoined: %v", err)
	}

	joinElapsed := time.Since(start)

	// Every round trip costs 2ms, so N+1 must be at least (queries-1) x 2ms
	// slower than the single-query version.
	if nPlusOneElapsed <= joinElapsed {
		t.Fatalf("N+1 (%s, %d queries) was not slower than the JOIN (%s)",
			nPlusOneElapsed, queries, joinElapsed)
	}

	if nPlusOneElapsed < time.Duration(queries-1)*2*time.Millisecond {
		t.Errorf("N+1 took %s for %d round trips at 2ms each", nPlusOneElapsed, queries)
	}
}

//
// WRITES
//

func TestAllInsertStrategiesWriteTheSameRows(t *testing.T) {
	const rows = 250

	for _, testCase := range []struct {
		name   string
		insert func(*db.Store, []db.Order) error
	}{
		{"one by one", func(s *db.Store, orders []db.Order) error {
			return s.InsertOneByOne(t.Context(), orders)
		}},
		{"transaction", func(s *db.Store, orders []db.Order) error {
			return s.InsertInTransaction(t.Context(), orders)
		}},
		{"batched", func(s *db.Store, orders []db.Order) error {
			_, err := s.InsertBatched(t.Context(), orders, 100)

			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := seeded(t, 1, 0)

			if err := testCase.insert(store, db.SampleOrders(rows, 1)); err != nil {
				t.Fatalf("insert: %v", err)
			}

			count, err := store.CountOrders(t.Context())
			if err != nil {
				t.Fatalf("count: %v", err)
			}

			if count != rows {
				t.Errorf("rows = %d, want %d", count, rows)
			}
		})
	}
}

func TestBatchedInsertChunks(t *testing.T) {
	store := seeded(t, 1, 0)

	statements, err := store.InsertBatched(t.Context(), db.SampleOrders(1000, 1), 250)
	if err != nil {
		t.Fatalf("InsertBatched: %v", err)
	}

	if statements != 4 {
		t.Errorf("statements = %d, want 4", statements)
	}
}

// A failed batch must leave nothing behind: the transaction is what makes a
// partial import impossible.
func TestFailedBatchRollsBack(t *testing.T) {
	store := seeded(t, 1, 0)

	orders := db.SampleOrders(10, 1)
	orders[7].CustomerID = 9999 // violates the foreign key

	if _, err := store.DB().ExecContext(t.Context(), `PRAGMA foreign_keys = ON;`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	if _, err := store.InsertBatched(t.Context(), orders, 5); err == nil {
		t.Fatal("expected the foreign key violation to fail the batch")
	}

	count, err := store.CountOrders(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 0 {
		t.Errorf("rows = %d after a failed batch, want 0", count)
	}
}

func TestBatchSizeIsBounded(t *testing.T) {
	store := seeded(t, 1, 0)

	// An absurd batch size is clamped rather than sent to the driver, where it
	// would blow the parameter limit.
	statements, err := store.InsertBatched(t.Context(), db.SampleOrders(1200, 1), 1_000_000)
	if err != nil {
		t.Fatalf("InsertBatched: %v", err)
	}

	if statements != 3 {
		t.Errorf("statements = %d, want 3 (clamped to %d rows)", statements, db.MaxBatchRows)
	}
}

func TestOrdersForCustomerFiltersByStatus(t *testing.T) {
	store := seeded(t, 20, 10)

	if err := store.Exec(t.Context(), db.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	orders, err := store.OrdersForCustomer(t.Context(), 1, "paid")
	if err != nil {
		t.Fatalf("OrdersForCustomer: %v", err)
	}

	for _, order := range orders {
		if order.CustomerID != 1 || order.Status != "paid" {
			t.Errorf("unexpected row: %+v", order)
		}
	}
}

func TestSplitStatementsIgnoresCommentedSemicolons(t *testing.T) {
	store := newStore(t)

	script := `
-- a comment with a semicolon; it must not split the statement
CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);
INSERT INTO notes (body) VALUES ('ok');`

	if err := store.Exec(t.Context(), script); err != nil {
		t.Fatalf("exec: %v", err)
	}

	var body string

	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT body FROM notes LIMIT 1;`).Scan(&body); err != nil {
		t.Fatalf("select: %v", err)
	}

	if body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}
