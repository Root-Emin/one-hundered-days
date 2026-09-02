// Day 90 - Performance: one complete cycle on the MVP.
//
// The loop, in the order it has to happen:
//
//	baseline -> profile -> fix ONE thing -> re-measure -> document -> guard
//
// Four rounds, each guided by the previous measurement:
//
//	v1  N+1 queries, no index      the honest first draft
//	v2  + the index                a migration; not one line of Go changed
//	v3  + one JOIN instead of N+1  the round trips go away
//	v4  + preallocated slices      some of the allocations go away
//
// The database runs in-process with a simulated 0.3ms round trip, because
// SQLite on localhost hides the exact cost this day is about.
//
// Run: go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	runtimepprof "runtime/pprof"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/api"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/perf"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/store"
)

const (
	customers         = 600
	ordersPerCustomer = 12
	pageSize          = 50
	loadWorkers       = 8
	loadRequests      = 240
	simulatedLatency  = 300 * time.Microsecond
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "day90")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", err)
		}
	}()

	handle, err := sql.Open("sqlite",
		"file:"+filepath.Join(dir, "day90.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	defer func() {
		if err := handle.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close db:", err)
		}
	}()

	dataStore := store.New(handle, simulatedLatency)

	if err := dataStore.Exec(ctx, store.Schema); err != nil {
		return err
	}

	if err := store.Seed(ctx, dataStore, customers, ordersPerCustomer); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := api.New(dataStore, logger)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	server := &http.Server{Handler: service.Routes(), ReadHeaderTimeout: 5 * time.Second}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "serve:", err)
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "shutdown:", err)
		}
	}()

	base := "http://" + listener.Addr().String()

	// A tuned client, so the load test measures the SERVER and not its own
	// TCP handshakes.
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        loadWorkers * 2,
			MaxIdleConnsPerHost: loadWorkers * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	section("0. The MVP")

	fmt.Printf("  %d customers, %d orders, page size %d\n", customers, customers*ordersPerCustomer, pageSize)
	fmt.Printf("  simulated database round trip: %s (SQLite is in-process; without this,\n", simulatedLatency)
	fmt.Println("  N+1 looks free right up until production, where the database is a hop away)")
	fmt.Printf("  load: %d requests, %d concurrent clients\n", loadRequests, loadWorkers)

	//
	// ROUND 1: BASELINE
	//

	section("1. Baseline (v1): load test the hot endpoint")

	v1, err := measure(ctx, client, service, base, api.V1)
	if err != nil {
		return err
	}

	printResult(v1.result, v1.queriesPerRequest)

	//
	// PROFILE
	//

	section("2. Profile: where does the time actually go?")

	cpuPath := filepath.Join(dir, "cpu-v1.pprof")
	heapPath := filepath.Join(dir, "heap-v1.pprof")

	if err := profileUnderLoad(ctx, client, base, api.V1, cpuPath, heapPath); err != nil {
		return err
	}

	cpuReport, err := pprofTop(ctx, cpuPath, 8, true, "Day-90/internal")
	if err != nil {
		return err
	}

	fmt.Println(indent(cpuReport))

	fmt.Println()
	fmt.Println("  the profile shows where CPU goes - but the handler is barely using")
	fmt.Println("  any. It is WAITING, and waiting does not appear in a CPU profile at")
	fmt.Println("  all. The X-Queries header is the number that explains the latency:")
	fmt.Printf("  %.0f queries per request at %s each is %s of pure round trips.\n",
		v1.queriesPerRequest, simulatedLatency,
		(time.Duration(v1.queriesPerRequest) * simulatedLatency).Round(time.Millisecond))

	heapReport, err := pprofTop(ctx, heapPath, 6, true, "Day-90/internal")
	if err != nil {
		return err
	}

	fmt.Println("\n  the heap profile, on the other hand, names its callers:")
	fmt.Println(indent(heapReport))

	//
	// ROUNDS 2-4
	//

	section("3. Fix one thing at a time, re-measuring after each")

	fmt.Println("  round 2: add the index. A migration - not one line of Go changes.")

	if err := dataStore.Exec(ctx, store.IndexSQL); err != nil {
		return err
	}

	plan, err := dataStore.Explain(ctx,
		`SELECT id, status, amount_cent, placed_at FROM orders WHERE customer_id = ? ORDER BY id;`, 1)
	if err != nil {
		return err
	}

	fmt.Printf("    plan is now: %s\n", plan)

	v2, err := measure(ctx, client, service, base, api.V2)
	if err != nil {
		return err
	}

	fmt.Println("\n  round 3: replace the N+1 with one JOIN.")

	v3, err := measure(ctx, client, service, base, api.V3)
	if err != nil {
		return err
	}

	fmt.Println("  round 4: preallocate the result slices (a pooled response buffer was")
	fmt.Println("  also tried here, measured worse, and reverted - see docs/PERF_REPORT.md).")

	v4, err := measure(ctx, client, service, base, api.V4)
	if err != nil {
		return err
	}

	//
	// DOCUMENT
	//

	section("4. Before and after")

	fmt.Printf("  %-26s %9s %9s %9s %9s %10s\n", "version", "queries", "p50", "p95", "p99", "req/s")

	for _, entry := range []measurement{v1, v2, v3, v4} {
		printRow(entry)
	}

	fmt.Println()
	fmt.Printf("  p95: %s -> %s  (%.1fx)\n",
		v1.result.P95.Round(time.Microsecond), v4.result.P95.Round(time.Microsecond),
		float64(v1.result.P95)/float64(v4.result.P95))
	fmt.Printf("  throughput: %.0f -> %.0f req/s  (%.1fx)\n",
		v1.result.Throughput, v4.result.Throughput, v4.result.Throughput/v1.result.Throughput)
	fmt.Printf("  queries per request: %.0f -> %.0f\n", v1.queriesPerRequest, v4.queriesPerRequest)

	fmt.Println()
	fmt.Printf("  round 2 (the index) was worth %.1fx, and round 3 (killing the N+1)\n",
		float64(v1.result.P95)/float64(v2.result.P95))
	fmt.Printf("  another %.1fx on top.\n", float64(v2.result.P95)/float64(v3.result.P95))

	round4 := float64(v3.result.P95) / float64(v4.result.P95)

	if round4 < 1.05 {
		fmt.Printf("  round 4 measured %.2fx end to end - which is noise, not a win.\n", round4)
		fmt.Println("  a load test cannot resolve a change this small; the allocation")
		fmt.Println("  benchmark can: BenchmarkDashboardV3 vs V4 reports 1933 -> 1868")
		fmt.Println("  allocs/op. Use the tool whose resolution matches the change.")
	} else {
		fmt.Printf("  round 4 added %.1fx on top of that.\n", round4)
	}
	fmt.Println()
	fmt.Println("  that ranking is the entire argument for measuring after every round.")
	fmt.Println("  ship all four together and you cannot tell which one paid - and the")
	fmt.Println("  temptation is always to credit the clever one (the pool) rather than")
	fmt.Println("  the boring one (51 round trips became 1).")

	//
	// GUARD
	//

	section("5. Guard against regressions")

	budget := perf.Budget{
		Name:                 "GET /dashboard",
		MaxP95:               3 * v4.result.P95,
		MaxQueriesPerRequest: 2,
		MinThroughput:        v4.result.Throughput / 3,
	}

	fmt.Printf("  budget: p95 < %s, queries/request <= %.0f, throughput > %.0f req/s\n",
		budget.MaxP95.Round(time.Millisecond), budget.MaxQueriesPerRequest, budget.MinThroughput)

	if err := budget.Check(v4.result, v4.queriesPerRequest, 0); err != nil {
		return fmt.Errorf("v4 fails its own budget: %w", err)
	}

	fmt.Println("  v4 passes.")

	// And the same budget against the old code, to prove the gate would
	// actually have caught the regression.
	err = budget.Check(v1.result, v1.queriesPerRequest, 0)

	fmt.Printf("\n  the same budget applied to v1: %v\n", errors.Is(err, perf.ErrBudgetExceeded))

	for _, line := range strings.Split(fmt.Sprint(err), "\n") {
		fmt.Println("    " + strings.TrimSpace(line))
	}

	fmt.Println()
	fmt.Println("  thresholds are set at 3x the measured value on purpose. The gate is")
	fmt.Println("  there to catch an N+1 coming back or an index dropped in a migration -")
	fmt.Println("  order-of-magnitude regressions. A gate that fails on 5% noise gets")
	fmt.Println("  disabled within a month, and then it protects nothing.")
	fmt.Println()
	fmt.Println("  the strongest guard on this page is the query count: it is exact, and")
	fmt.Println("  it does not vary with the machine, the load or the scheduler.")
	fmt.Println("  internal/api/api_test.go asserts it, so an N+1 fails in CI.")

	fmt.Println("\n  the full write-up is in docs/PERF_REPORT.md")

	return nil
}

type measurement struct {
	version           api.Version
	result            perf.Result
	queriesPerRequest float64
}

func measure(ctx context.Context, client *http.Client, service *api.API, base string, version api.Version) (measurement, error) {
	service.Reset()

	url := fmt.Sprintf("%s/dashboard?v=%d&limit=%d&tier=pro", base, version, pageSize)

	// A short warm-up: the first requests pay for connection setup and a cold
	// page cache, and including them measures the wrong thing.
	if _, err := perf.Run(ctx, client, perf.Options{
		Label: "warmup", URL: url, Workers: loadWorkers, Requests: loadWorkers,
	}); err != nil {
		return measurement{}, err
	}

	service.Reset()

	result, err := perf.Run(ctx, client, perf.Options{
		Label:    version.String(),
		URL:      url,
		Workers:  loadWorkers,
		Requests: loadRequests,
	})
	if err != nil {
		return measurement{}, err
	}

	samples := service.Stats(version)

	queriesPerRequest := 0.0

	if samples.Requests > 0 {
		queriesPerRequest = float64(samples.Queries) / float64(samples.Requests)
	}

	return measurement{version: version, result: result, queriesPerRequest: queriesPerRequest}, nil
}

func printResult(result perf.Result, queriesPerRequest float64) {
	fmt.Printf("  %-26s %9s %9s %9s %9s %10s\n", "version", "queries", "p50", "p95", "p99", "req/s")
	printRow(measurement{result: result, queriesPerRequest: queriesPerRequest})
}

func printRow(entry measurement) {
	fmt.Printf("  %-26s %9.0f %9s %9s %9s %10.0f\n",
		entry.result.Label,
		entry.queriesPerRequest,
		entry.result.P50.Round(time.Microsecond),
		entry.result.P95.Round(time.Microsecond),
		entry.result.P99.Round(time.Microsecond),
		entry.result.Throughput)
}

// profileUnderLoad captures CPU and heap profiles WHILE traffic is running.
// A profile of an idle process is a profile of the scheduler.
func profileUnderLoad(ctx context.Context, client *http.Client, base string, version api.Version, cpuPath, heapPath string) error {
	url := fmt.Sprintf("%s/dashboard?v=%d&limit=%d&tier=pro", base, version, pageSize)

	cpuFile, err := os.Create(cpuPath) //nolint:gosec // path is built by this program
	if err != nil {
		return fmt.Errorf("create cpu profile: %w", err)
	}

	defer func() {
		if err := cpuFile.Close(); err != nil {
			_ = err
		}
	}()

	if err := runtimepprof.StartCPUProfile(cpuFile); err != nil {
		return fmt.Errorf("start cpu profile: %w", err)
	}

	if _, err := perf.Run(ctx, client, perf.Options{
		Label: "profile", URL: url, Workers: loadWorkers, Requests: loadRequests,
	}); err != nil {
		runtimepprof.StopCPUProfile()

		return err
	}

	runtimepprof.StopCPUProfile()

	heapFile, err := os.Create(heapPath) //nolint:gosec // path is built by this program
	if err != nil {
		return fmt.Errorf("create heap profile: %w", err)
	}

	defer func() {
		if err := heapFile.Close(); err != nil {
			_ = err
		}
	}()

	// Force a collection first, or the profile counts garbage that simply has
	// not been swept yet and every allocation looks like a leak.
	runtime.GC()

	if err := runtimepprof.WriteHeapProfile(heapFile); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}

	return nil
}

func pprofTop(ctx context.Context, path string, nodes int, cumulative bool, focus string) (string, error) {
	args := []string{"tool", "pprof", "-top", fmt.Sprintf("-nodecount=%d", nodes)}

	if cumulative {
		args = append(args, "-cum")
	}

	if focus != "" {
		args = append(args, "-focus="+focus)
	}

	args = append(args, path)

	output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("go tool pprof: %w", err)
	}

	return shortenPackages(string(output)), nil
}

// shortenPackages trims the long module prefix so the report fits a terminal.
func shortenPackages(report string) string {
	const prefix = "example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/"

	return strings.ReplaceAll(report, prefix, "")
}

func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	for i, line := range lines {
		lines[i] = "  " + line
	}

	return strings.Join(lines, "\n")
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
