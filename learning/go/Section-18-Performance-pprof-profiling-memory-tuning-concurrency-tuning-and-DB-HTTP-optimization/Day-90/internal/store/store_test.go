package store_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/store"
)

func newStore(t *testing.T, latency time.Duration) *store.Store {
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

	dataStore := store.New(handle, latency)

	if err := dataStore.Exec(t.Context(), store.Schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	if err := store.Seed(t.Context(), dataStore, 60, 5); err != nil {
		t.Fatalf("seed: %v", err)
	}

	return dataStore
}

// All four versions must produce the same page, including the computed totals.
func TestNPlusOneAndJoinAgree(t *testing.T) {
	dataStore := newStore(t, 0)

	nPlusOne, nQueries, err := dataStore.DashboardNPlusOne(t.Context(), "pro", 15)
	if err != nil {
		t.Fatalf("DashboardNPlusOne: %v", err)
	}

	if len(nPlusOne) == 0 {
		t.Fatal("the fixture produced no rows")
	}

	for _, preallocate := range []bool{false, true} {
		joined, joinQueries, err := dataStore.DashboardJoined(t.Context(), "pro", 15, preallocate)
		if err != nil {
			t.Fatalf("DashboardJoined(preallocate=%t): %v", preallocate, err)
		}

		if joinQueries.Count != 1 {
			t.Errorf("preallocate=%t: %d queries, want 1", preallocate, joinQueries.Count)
		}

		if len(joined) != len(nPlusOne) {
			t.Fatalf("preallocate=%t: %d rows, want %d", preallocate, len(joined), len(nPlusOne))
		}

		for i := range nPlusOne {
			want, got := nPlusOne[i], joined[i]

			if got.Customer.ID != want.Customer.ID {
				t.Fatalf("row %d: customer %d, want %d", i, got.Customer.ID, want.Customer.ID)
			}

			if got.TotalCent != want.TotalCent {
				t.Errorf("customer %d: total %d, want %d", got.Customer.ID, got.TotalCent, want.TotalCent)
			}

			if got.OrderCount != want.OrderCount {
				t.Errorf("customer %d: order count %d, want %d",
					got.Customer.ID, got.OrderCount, want.OrderCount)
			}
		}
	}

	if nQueries.Count != len(nPlusOne)+1 {
		t.Errorf("N+1 issued %d queries for %d rows, want %d", nQueries.Count, len(nPlusOne), len(nPlusOne)+1)
	}
}

// The index has to change the plan, not merely exist. This is the assertion
// that catches a migration silently dropping it.
func TestIndexIsUsedByTheHotQuery(t *testing.T) {
	dataStore := newStore(t, 0)

	query := `SELECT id, status, amount_cent, placed_at FROM orders WHERE customer_id = ? ORDER BY id;`

	before, err := dataStore.Explain(t.Context(), query, 1)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if strings.Contains(before, "SEARCH") {
		t.Errorf("plan seeks before the index exists: %s", before)
	}

	if err := dataStore.Exec(t.Context(), store.IndexSQL); err != nil {
		t.Fatalf("create index: %v", err)
	}

	after, err := dataStore.Explain(t.Context(), query, 1)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if !strings.Contains(after, "SEARCH") {
		t.Errorf("plan does not seek with the index: %s", after)
	}
}

// A customer with no orders must survive the LEFT JOIN - the NULL columns are
// what make this different from an INNER JOIN.
func TestJoinKeepsCustomersWithNoOrders(t *testing.T) {
	dataStore := newStore(t, 0)

	if _, err := dataStore.DB().ExecContext(t.Context(),
		`INSERT INTO customers (name, tier) VALUES ('empty', 'vip');`); err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	rows, _, err := dataStore.DashboardJoined(t.Context(), "vip", 10, true)
	if err != nil {
		t.Fatalf("DashboardJoined: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	if rows[0].OrderCount != 0 || rows[0].TotalCent != 0 {
		t.Errorf("row = %+v, want an empty order list", rows[0])
	}
}

// The N+1 cost is the round-trip COUNT, which only becomes visible once a
// query costs something. This asserts the mechanism, not a wall-clock number.
func TestLatencyMakesTheRoundTripsVisible(t *testing.T) {
	dataStore := newStore(t, time.Millisecond)

	start := time.Now()

	rows, queries, err := dataStore.DashboardNPlusOne(t.Context(), "pro", 10)
	if err != nil {
		t.Fatalf("DashboardNPlusOne: %v", err)
	}

	nPlusOne := time.Since(start)

	start = time.Now()

	if _, _, err := dataStore.DashboardJoined(t.Context(), "pro", 10, true); err != nil {
		t.Fatalf("DashboardJoined: %v", err)
	}

	joined := time.Since(start)

	if queries.Count != len(rows)+1 {
		t.Fatalf("queries = %d for %d rows", queries.Count, len(rows))
	}

	if nPlusOne < time.Duration(queries.Count-1)*time.Millisecond {
		t.Errorf("N+1 took %s for %d round trips at 1ms each", nPlusOne, queries.Count)
	}

	if nPlusOne <= joined {
		t.Errorf("N+1 (%s) was not slower than the single query (%s)", nPlusOne, joined)
	}
}

func TestSplitStatementsIgnoresCommentedSemicolons(t *testing.T) {
	dataStore := newStore(t, 0)

	script := `
-- a comment containing a semicolon; it must not split the statement
CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);
INSERT INTO notes (body) VALUES ('ok');`

	if err := dataStore.Exec(t.Context(), script); err != nil {
		t.Fatalf("exec: %v", err)
	}

	var body string

	if err := dataStore.DB().QueryRowContext(t.Context(), `SELECT body FROM notes;`).Scan(&body); err != nil {
		t.Fatalf("select: %v", err)
	}

	if body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}
