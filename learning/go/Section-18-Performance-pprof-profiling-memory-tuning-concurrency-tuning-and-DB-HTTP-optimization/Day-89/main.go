// Day 89 - Performance: database and HTTP.
//
// Most "slow service" tickets are not CPU. They are round trips: a query the
// planner turned into a full scan, a loop issuing one query per row, a client
// re-handshaking a TCP connection per call, and a dependency with no timeout
// holding a goroutine hostage.
//
// Everything below is measured against a real SQLite database and a real HTTP
// server on localhost. Localhost has microsecond latency, so the ratios here
// are the FLOOR - across a network they get dramatically worse.
//
// Run: go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-89
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-89/internal/db"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-89/internal/httpclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "day89")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", err)
		}
	}()

	// A file, not :memory: - the point of today is round trips and durability
	// costs, and an in-memory database has neither.
	handle, err := sql.Open("sqlite",
		"file:"+filepath.Join(dir, "day89.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	defer func() {
		if err := handle.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close db:", err)
		}
	}()

	store := db.New(handle)

	if err := store.Exec(ctx, db.Schema); err != nil {
		return err
	}

	const (
		customers         = 400
		ordersPerCustomer = 25
	)

	start := time.Now()

	if err := db.Seed(ctx, store, customers, ordersPerCustomer); err != nil {
		return err
	}

	total, err := store.CountOrders(ctx)
	if err != nil {
		return err
	}

	section("0. The data")

	fmt.Printf("  %d customers, %d orders, seeded in %s\n",
		customers, total, time.Since(start).Round(time.Millisecond))

	if err := demoQueryPlans(ctx, store); err != nil {
		return err
	}

	if err := demoNPlusOne(ctx, store); err != nil {
		return err
	}

	if err := demoBatchWrites(ctx, store); err != nil {
		return err
	}

	if err := demoKeepAlive(ctx); err != nil {
		return err
	}

	return demoTimeouts(ctx)
}

//
// 1. QUERY PLANS
//

func demoQueryPlans(ctx context.Context, store *db.Store) error {
	section("1. EXPLAIN: is the index actually used?")

	// Start without the index, so the plan and the timing are the honest
	// "before".
	if err := store.Exec(ctx, db.DropIndexSQL); err != nil {
		return err
	}

	before, err := store.Explain(ctx, db.OrdersForCustomerSQL, 7, "paid")
	if err != nil {
		return err
	}

	beforeDuration := timeQuery(ctx, store, 200)

	fmt.Println("  without the index:")
	fmt.Printf("    plan: %s\n", indent(before))
	fmt.Printf("    seeks (SEARCH) rather than scans: %t\n", db.Seeks(before))
	fmt.Printf("    200 lookups: %s\n", beforeDuration.Round(time.Microsecond))

	if err := store.Exec(ctx, db.IndexSQL); err != nil {
		return err
	}

	after, err := store.Explain(ctx, db.OrdersForCustomerSQL, 7, "paid")
	if err != nil {
		return err
	}

	afterDuration := timeQuery(ctx, store, 200)

	fmt.Println("\n  with idx_orders_customer_status:")
	fmt.Printf("    plan: %s\n", indent(after))
	fmt.Printf("    seeks (SEARCH) rather than scans: %t\n", db.Seeks(after))
	fmt.Printf("    200 lookups: %s  (%.1fx faster)\n",
		afterDuration.Round(time.Microsecond),
		float64(beforeDuration)/float64(afterDuration))

	// A function around a column disables the index FOR THAT COLUMN. Here the
	// index is still used for customer_id - the plan says (customer_id=?)
	// only - so the database now reads every order of that customer and
	// filters them in memory. The query still returns the right answer, which
	// is why this survives review.
	partial, err := store.Explain(ctx,
		`SELECT id FROM orders WHERE customer_id = ? AND UPPER(status) = ?;`, 7, "PAID")
	if err != nil {
		return err
	}

	fmt.Println("\n  the same query with UPPER(status) around the column:")
	fmt.Printf("    plan: %s\n", indent(partial))
	fmt.Println("    the plan now says (customer_id=?) only: the status half of the index")
	fmt.Println("    is unusable, so every order of that customer is read and filtered")

	// On the leading column there is nothing left to search with, so the index
	// is abandoned entirely and the plan falls back to a scan.
	dropped, err := store.Explain(ctx,
		`SELECT id FROM orders WHERE DATE(created_at) = ?;`, "2026-03-01")
	if err != nil {
		return err
	}

	fmt.Println("\n  a function around the LEADING column of idx_orders_created_at:")
	fmt.Printf("    plan: %s\n", indent(dropped))
	fmt.Println("    the index is abandoned entirely - store the value you search by,")
	fmt.Println("    or build an index on the expression itself")

	// And the left-to-right rule.
	statusOnly, err := store.Explain(ctx,
		`SELECT id FROM orders WHERE status = ?;`, "paid")
	if err != nil {
		return err
	}

	fmt.Println("\n  filtering on status alone, with an index on (customer_id, status):")
	fmt.Printf("    plan: %s\n", indent(statusOnly))
	fmt.Printf("    touches an index: %t, but seeks: %t\n",
		db.UsesIndex(statusOnly), db.Seeks(statusOnly))
	fmt.Println("    SCAN ... USING COVERING INDEX reads the whole index end to end. It")
	fmt.Println("    avoids the table, so it is cheaper - but it is still O(n), not a seek.")
	fmt.Println("    an index is usable left-to-right, like a phone book sorted by surname")
	fmt.Println("    then first name: no help at all for finding every 'Ada'.")
	fmt.Println("    this is why 'we have an index' and 'the query is fast' are different")
	fmt.Println("    claims, and only EXPLAIN settles the second one.")

	return nil
}

func timeQuery(ctx context.Context, store *db.Store, iterations int) time.Duration {
	start := time.Now()

	for i := 0; i < iterations; i++ {
		if _, err := store.OrdersForCustomer(ctx, int64(i%400)+1, "paid"); err != nil {
			return 0
		}
	}

	return time.Since(start)
}

//
// 2. N+1
//

func demoNPlusOne(ctx context.Context, store *db.Store) error {
	section("2. N+1 versus one round trip")

	const pageSize = 100

	// First with the database in-process, where a query costs microseconds.
	local, err := compareStrategies(ctx, store, pageSize)
	if err != nil {
		return err
	}

	fmt.Println("  SQLite, in-process (a query costs microseconds):")
	printStrategies(local)

	fmt.Println()
	fmt.Println("  barely a difference - and this is the trap. An embedded database has")
	fmt.Println("  no round trip to pay for, so N+1 looks free right up until the data")
	fmt.Println("  moves to another host.")

	// Now with a simulated 0.5 ms round trip - a modest number for a database
	// on another machine in the same datacentre.
	store.SetLatency(500 * time.Microsecond)

	defer store.SetLatency(0)

	remote, err := compareStrategies(ctx, store, pageSize)
	if err != nil {
		return err
	}

	fmt.Println("\n  the same code with a simulated 0.5ms network round trip:")
	printStrategies(remote)

	fmt.Println()
	fmt.Println("  now the query COUNT is the latency. 96 round trips at half a")
	fmt.Println("  millisecond is 48ms of waiting, in a page that does almost no work.")
	fmt.Println("  the JOIN and the IN(...) versions barely moved, because they still")
	fmt.Println("  make one and two round trips no matter how many rows come back.")
	fmt.Println()
	fmt.Println("  JOIN or batch? the JOIN sends the parent columns once per child row;")
	fmt.Println("  the two-query version does not, and it still works when the children")
	fmt.Println("  live in a different database or behind a different service.")

	return nil
}

type strategy struct {
	name     string
	queries  int
	duration time.Duration
}

func compareStrategies(ctx context.Context, store *db.Store, pageSize int) ([]strategy, error) {
	nPlusOne, queries, err := timeLoad(ctx, func() (int, int, error) {
		result, queries, err := store.LoadNPlusOne(ctx, "TR", pageSize)

		return len(result), queries, err
	})
	if err != nil {
		return nil, err
	}

	joined, joinQueries, err := timeLoad(ctx, func() (int, int, error) {
		result, queries, err := store.LoadJoined(ctx, "TR", pageSize)

		return len(result), queries, err
	})
	if err != nil {
		return nil, err
	}

	batched, batchQueries, err := timeLoad(ctx, func() (int, int, error) {
		result, queries, err := store.LoadBatched(ctx, "TR", pageSize)

		return len(result), queries, err
	})
	if err != nil {
		return nil, err
	}

	return []strategy{
		{"N+1", queries, nPlusOne},
		{"one JOIN", joinQueries, joined},
		{"two queries + IN(...)", batchQueries, batched},
	}, nil
}

func printStrategies(strategies []strategy) {
	fmt.Printf("  %-24s %8s %12s %s\n", "strategy", "queries", "duration", "")

	baseline := strategies[0].duration

	for i, entry := range strategies {
		speedup := ""

		if i > 0 && entry.duration > 0 {
			speedup = fmt.Sprintf("  %.1fx faster", float64(baseline)/float64(entry.duration))
		}

		fmt.Printf("  %-24s %8d %12s%s\n",
			entry.name, entry.queries, entry.duration.Round(time.Microsecond), speedup)
	}
}

func timeLoad(ctx context.Context, load func() (int, int, error)) (time.Duration, int, error) {
	_ = ctx

	start := time.Now()

	_, queries, err := load()
	if err != nil {
		return 0, 0, err
	}

	return time.Since(start), queries, nil
}

//
// 3. BATCHED WRITES
//

func demoBatchWrites(ctx context.Context, store *db.Store) error {
	section("3. Batching writes")

	const rows = 2000

	orders := db.SampleOrders(rows, 1)

	start := time.Now()

	if err := store.InsertOneByOne(ctx, orders); err != nil {
		return err
	}

	oneByOne := time.Since(start)

	start = time.Now()

	if err := store.InsertInTransaction(ctx, orders); err != nil {
		return err
	}

	inTransaction := time.Since(start)

	start = time.Now()

	statements, err := store.InsertBatched(ctx, orders, 500)
	if err != nil {
		return err
	}

	batched := time.Since(start)

	fmt.Printf("  %d rows\n\n", rows)
	fmt.Printf("  %-34s %12s %10s %s\n", "strategy", "duration", "stmts", "")
	fmt.Printf("  %-34s %12s %10d\n", "one INSERT per row", oneByOne.Round(time.Millisecond), rows)
	fmt.Printf("  %-34s %12s %10d  (%.1fx)\n", "one transaction, prepared once",
		inTransaction.Round(time.Millisecond), rows, float64(oneByOne)/float64(inTransaction))
	fmt.Printf("  %-34s %12s %10d  (%.1fx)\n", "multi-row INSERT, 500 per stmt",
		batched.Round(time.Millisecond), statements, float64(oneByOne)/float64(batched))

	fmt.Println()
	fmt.Println("  the first win is the transaction: one commit and one disk sync")
	fmt.Println("  instead of 2000. The second is fewer statements to parse and plan.")
	fmt.Printf("  keep batches bounded (%d rows here): drivers cap parameters -\n", db.MaxBatchRows)
	fmt.Println("  32,766 for SQLite, 65,535 for the PostgreSQL wire protocol.")

	return nil
}

//
// 4. KEEP-ALIVE
//

func demoKeepAlive(ctx context.Context) error {
	section("4. HTTP keep-alive")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(strings.Repeat("x", 512))); err != nil {
			_ = err
		}
	}))

	defer server.Close()

	const requests = 300

	measure := func(config httpclient.Config, drain bool) (time.Duration, *httpclient.Stats, error) {
		client := httpclient.New(config)
		stats := &httpclient.Stats{}

		start := time.Now()

		for i := 0; i < requests; i++ {
			if drain {
				if _, err := httpclient.Get(ctx, client, server.URL, stats); err != nil {
					return 0, nil, err
				}

				continue
			}

			if err := httpclient.GetWithoutDraining(ctx, client, server.URL, stats); err != nil {
				return 0, nil, err
			}
		}

		return time.Since(start), stats, nil
	}

	noKeepAlive := httpclient.DefaultConfig()
	noKeepAlive.DisableKeepAlives = true

	coldDuration, coldStats, err := measure(noKeepAlive, true)
	if err != nil {
		return err
	}

	warmDuration, warmStats, err := measure(httpclient.DefaultConfig(), true)
	if err != nil {
		return err
	}

	undrainedDuration, undrainedStats, err := measure(httpclient.DefaultConfig(), false)
	if err != nil {
		return err
	}

	fmt.Printf("  %d requests to a localhost server\n\n", requests)
	fmt.Printf("  %-34s %12s %10s %10s %s\n", "client", "duration", "new", "reused", "reuse")
	printConn("DisableKeepAlives: true", coldDuration, coldStats)
	printConn("keep-alive (default)", warmDuration, warmStats)
	printConn("keep-alive, body NOT drained", undrainedDuration, undrainedStats)

	fmt.Println()
	fmt.Printf("  keep-alive is %.1fx faster here, on localhost, with no TLS.\n",
		float64(coldDuration)/float64(warmDuration))
	fmt.Println("  add a real network and a TLS handshake and the gap is an order of")
	fmt.Println("  magnitude - the handshake alone is two round trips before any data.")
	fmt.Println()
	fmt.Println("  the third row is the trap: keep-alive is ON, but closing a response")
	fmt.Println("  body without reading it leaves bytes in flight, so the connection")
	fmt.Println("  cannot go back in the pool. Always drain, then close.")
	fmt.Println()
	fmt.Println("  the other setting people miss: MaxIdleConnsPerHost defaults to 2.")
	fmt.Println("  50 concurrent calls to one host keeps 2 connections and re-handshakes")
	fmt.Println("  the other 48 every single time.")

	return nil
}

func printConn(name string, duration time.Duration, stats *httpclient.Stats) {
	fmt.Printf("  %-34s %12s %10d %10d %9.0f%%\n",
		name, duration.Round(time.Millisecond),
		stats.NewConns.Load(), stats.ReusedConns.Load(), stats.ReuseRate()*100)
}

//
// 5. TIMEOUTS
//

func demoTimeouts(ctx context.Context) error {
	section("5. Timeouts everywhere")

	// A server that accepts the connection and then never answers - the exact
	// behaviour that hangs a caller with no timeout.
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Second):
			if _, err := w.Write([]byte("too late")); err != nil {
				_ = err
			}
		case <-r.Context().Done():
		}
	}))

	defer stuck.Close()

	fmt.Println("  a dependency that accepts the connection and never replies:")

	config := httpclient.DefaultConfig()
	config.Timeouts.ResponseHeader = 200 * time.Millisecond

	client := httpclient.New(config)

	start := time.Now()

	_, err := httpclient.Get(ctx, client, stuck.URL, nil)

	fmt.Printf("    ResponseHeaderTimeout=200ms -> failed after %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Printf("    error: %v\n", trimError(err))

	// The per-request context: the deadline the CALLER sets, which is the one
	// that should win when it is shorter.
	requestCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	start = time.Now()

	_, err = httpclient.Get(requestCtx, httpclient.New(httpclient.DefaultConfig()), stuck.URL, nil)

	fmt.Printf("\n    caller's context deadline=50ms -> failed after %s\n",
		time.Since(start).Round(time.Millisecond))
	fmt.Printf("    is DeadlineExceeded: %t\n", errors.Is(err, context.DeadlineExceeded))

	fmt.Println()
	fmt.Println("  http.DefaultClient has NO timeout at all. A hung dependency holds")
	fmt.Println("  the goroutine, its stack, and the connection until the TCP stack")
	fmt.Println("  gives up - which can be minutes. Enough of those and the service is")
	fmt.Println("  out of workers while every dashboard says the CPU is idle.")
	fmt.Println()
	fmt.Println("  the four layers, and why they are not one knob:")
	fmt.Println("    Dial            2s   the host is down -> fail fast")
	fmt.Println("    TLSHandshake    2s   the certificate exchange")
	fmt.Println("    ResponseHeader  3s   the server is thinking -> the one that catches hangs")
	fmt.Println("    Client.Timeout 10s   everything, including reading the body")
	fmt.Println("  a single Client.Timeout also caps a legitimate 5-minute download.")
	fmt.Println("  the caller's context deadline should override all of them when shorter.")

	return nil
}

func trimError(err error) string {
	if err == nil {
		return "<nil>"
	}

	message := err.Error()

	if index := strings.Index(message, ": "); index > 0 && len(message) > 80 {
		return "..." + message[index+2:]
	}

	return message
}

func indent(plan string) string {
	return strings.ReplaceAll(plan, "\n", "\n          ")
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
