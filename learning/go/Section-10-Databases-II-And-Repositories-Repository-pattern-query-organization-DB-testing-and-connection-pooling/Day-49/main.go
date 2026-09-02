package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

/*
Day 49 - Databases (II) & Repositories: Connection Pooling

Tasks covered:

 1. Tune SetMaxOpenConns / SetMaxIdleConns / lifetimes (pool.go)
 2. Watch saturation: wait counts and wait time, sampled and reported
 3. Close the pool during graceful shutdown
 4. Driver notes: what changes between SQLite, Postgres and MySQL (below)

Run:

	go run .            # sweep pool sizes under a fixed concurrent load
	go run . serve      # a server that monitors its pool and closes it on SIGINT

Environment variables:

	WORKERS   concurrent callers in the sweep.   Default: 32
	QUERIES   queries per worker.                Default: 6
	DB_PATH   SQLite path.                       Default: :memory:

	DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS,
	DB_CONN_MAX_LIFETIME, DB_CONN_MAX_IDLE_TIME   used by `serve`

Test:

	go test ./...
*/

const Schema = `
CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	payload TEXT NOT NULL
);`

// slowQuery burns a measurable amount of time inside the database so that
// connections stay checked out long enough for queueing to be visible. On
// Postgres you would use pg_sleep(0.05) for the same effect.
const slowQuery = `
	WITH RECURSIVE counter(x) AS (
		SELECT 1
		UNION ALL
		SELECT x + 1 FROM counter WHERE x < 40000
	)
	SELECT COUNT(*) FROM counter;`

type loadResult struct {
	PoolSize    int
	Duration    time.Duration
	Throughput  float64
	P50         time.Duration
	P95         time.Duration
	WaitCount   int64
	WaitTime    time.Duration
	MaxInUse    int
	Saturations int
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day49: ")

	ctx := context.Background()

	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(ctx); err != nil {
			log.Fatalf("serve: %v", err)
		}

		return
	}

	workers := envInt("WORKERS", 32)
	queries := envInt("QUERIES", 6)

	fmt.Printf("\n%d concurrent workers x %d queries = %d queries per run\n",
		workers, queries, workers*queries)
	fmt.Println(strings.Repeat("-", 92))
	fmt.Printf("%-10s %-11s %-13s %-10s %-10s %-9s %-12s %s\n",
		"MAX OPEN", "DURATION", "QUERIES/SEC", "P50", "P95", "WAITS", "WAIT TIME", "SATURATED SAMPLES")

	var results []loadResult

	for _, poolSize := range []int{1, 2, 4, 8, 16, 32} {
		result, err := runLoad(ctx, poolSize, workers, queries)
		if err != nil {
			log.Fatalf("pool size %d: %v", poolSize, err)
		}

		results = append(results, result)

		fmt.Printf("%-10d %-11s %-13.0f %-10s %-10s %-9d %-12s %d\n",
			result.PoolSize,
			result.Duration.Round(time.Millisecond),
			result.Throughput,
			result.P50.Round(time.Millisecond),
			result.P95.Round(time.Millisecond),
			result.WaitCount,
			result.WaitTime.Round(time.Millisecond),
			result.Saturations,
		)
	}

	best := results[0]

	for _, result := range results[1:] {
		if result.Throughput > best.Throughput {
			best = result
		}
	}

	fmt.Printf("\nBest throughput at max_open=%d (%.0f queries/sec).\n", best.PoolSize, best.Throughput)
	fmt.Println("Small pools queue: waits climb and p95 latency goes with them.")
	fmt.Println("Past the point where the database itself is the bottleneck, a bigger")
	fmt.Println("pool stops helping and only moves the queue from your process onto")
	fmt.Println("the database server - where it hurts every other client too.")

	printDriverNotes()
}

// runLoad drives `workers` goroutines against a pool of the given size and
// reports both client-side latency and pool-side pressure.
func runLoad(ctx context.Context, poolSize, workers, queriesPerWorker int) (loadResult, error) {
	config := PoolConfig{
		MaxOpenConns:    poolSize,
		MaxIdleConns:    poolSize,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}

	db, err := OpenPool(ctx, envOr("DB_PATH", ":memory:"), config)
	if err != nil {
		return loadResult{}, err
	}

	defer func() {
		if err := ClosePool(db); err != nil {
			log.Printf("close pool: %v", err)
		}
	}()

	monitor := NewPoolMonitor(db, 5*time.Millisecond)

	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()

	// The monitor is a goroutine with an owner and a stop signal, not a
	// fire-and-forget one.
	monitorDone := make(chan struct{})

	go func() {
		defer close(monitorDone)

		monitor.Run(monitorCtx)
	}()

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		latencies []time.Duration
		failures  []error
	)

	start := time.Now()

	for range workers {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			for range queriesPerWorker {
				queryStart := time.Now()

				var count int64

				// The context deadline covers the whole call, including the
				// time spent waiting for a free connection. That is exactly
				// how pool exhaustion turns into request timeouts.
				queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				err := db.QueryRowContext(queryCtx, slowQuery).Scan(&count)

				cancel()

				mu.Lock()

				if err != nil {
					failures = append(failures, err)
				} else {
					latencies = append(latencies, time.Since(queryStart))
				}

				mu.Unlock()
			}
		}()
	}

	waitGroup.Wait()

	elapsed := time.Since(start)

	stopMonitor()
	<-monitorDone

	if len(failures) > 0 {
		return loadResult{}, fmt.Errorf("%d queries failed, first: %w", len(failures), failures[0])
	}

	stats := db.Stats()

	maxInUse := 0

	for _, sample := range monitor.Samples() {
		if sample.InUse > maxInUse {
			maxInUse = sample.InUse
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	return loadResult{
		PoolSize:    poolSize,
		Duration:    elapsed,
		Throughput:  float64(len(latencies)) / elapsed.Seconds(),
		P50:         percentile(latencies, 0.50),
		P95:         percentile(latencies, 0.95),
		WaitCount:   stats.WaitCount,
		WaitTime:    stats.WaitDuration,
		MaxInUse:    maxInUse,
		Saturations: monitor.SaturatedSamples(),
	}, nil
}

func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	index := int(float64(len(sorted)-1) * fraction)

	return sorted[index]
}

// serve is the shutdown half of today's tasks: a pool that is monitored while
// the process runs and closed before it exits.
func serve(ctx context.Context) error {
	config := DefaultPoolConfig()

	log.Printf("pool config: %s", config)

	db, err := OpenPool(ctx, envOr("DB_PATH", ":memory:"), config)
	if err != nil {
		return err
	}

	monitor := NewPoolMonitor(db, time.Second)

	monitorCtx, stopMonitor := context.WithCancel(ctx)

	monitorDone := make(chan struct{})

	go func() {
		defer close(monitorDone)

		monitor.Run(monitorCtx)
	}()

	// A tiny background workload so the pool has something to report.
	workCtx, stopWork := context.WithCancel(ctx)

	workDone := make(chan struct{})

	go func() {
		defer close(workDone)

		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-workCtx.Done():
				return

			case <-ticker.C:
				var count int64

				if err := db.QueryRowContext(workCtx, slowQuery).Scan(&count); err != nil &&
					!errors.Is(err, context.Canceled) {
					log.Printf("background query: %v", err)
				}
			}
		}
	}()

	log.Printf("running; press CTRL+C to shut down")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	received := <-shutdown

	log.Printf("shutdown signal: %s", received)

	// Order matters: stop producing work, let it drain, stop the monitor,
	// then close the pool. Closing first would abort in-flight queries.
	stopWork()
	<-workDone

	stopMonitor()
	<-monitorDone

	return ClosePool(db)
}

func printDriverNotes() {
	fmt.Println("\nDriver notes")
	fmt.Println(strings.Repeat("-", 92))

	notes := []struct {
		driver string
		note   string
	}{
		{
			"pgx (Postgres)",
			"Each connection is a server-side process: max_open across all replicas must stay under max_connections. Use PgBouncer in transaction mode for high fan-out, and keep prepared statements in mind - they do not survive a pooled connection switch.",
		},
		{
			"lib/pq (Postgres)",
			"Older, no built-in binary protocol tuning. Always set ConnMaxLifetime below any server or proxy idle timeout, or you will hand out dead connections.",
		},
		{
			"go-sql-driver/mysql",
			"MySQL closes idle connections after wait_timeout (8h default, often minutes in managed setups). ConnMaxLifetime must be shorter, or queries fail with 'invalid connection'.",
		},
		{
			"modernc.org/sqlite (this program)",
			"In-process, so 'connections' are cheap, but writes serialise on one write lock. WAL mode plus busy_timeout is the tuning that matters; a large pool mostly adds contention.",
		},
	}

	for _, note := range notes {
		fmt.Printf("  %s\n", note.driver)
		fmt.Printf("    %s\n\n", wrap(note.note, 84, "    "))
	}
}

func wrap(text string, width int, indent string) string {
	var (
		builder strings.Builder
		line    int
	)

	for _, word := range strings.Fields(text) {
		if line > 0 && line+len(word)+1 > width {
			builder.WriteString("\n" + indent)
			line = 0
		} else if line > 0 {
			builder.WriteString(" ")
			line++
		}

		builder.WriteString(word)
		line += len(word)
	}

	return builder.String()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
