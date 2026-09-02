// Day 88 - Performance: concurrency tuning.
//
// Four questions, answered with measurements rather than instinct:
//
//  1. how many workers?      sweep the count; the answer differs by an order
//     of magnitude between CPU-bound and I/O-bound work
//  2. did they all exit?     goroutine counts that climb and never fall
//  3. who cancels whom?      errgroup propagates the first error
//  4. what is stuck?         the goroutine profile names the exact line
//
// Run: go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/fanout"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/leak"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/pipeline"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	procs := runtime.GOMAXPROCS(0)

	section("0. The machine")

	fmt.Printf("  GOMAXPROCS=%d  NumCPU=%d  goroutines at rest=%d\n",
		procs, runtime.NumCPU(), leak.Count())
	fmt.Println("  GOMAXPROCS is the number of goroutines that can execute Go code at")
	fmt.Println("  once. It is a ceiling on parallelism, not on concurrency.")

	if err := demoWorkerCounts(ctx, procs); err != nil {
		return err
	}

	if err := demoLeaks(); err != nil {
		return err
	}

	if err := demoErrgroup(ctx); err != nil {
		return err
	}

	return nil
}

//
// 1. RIGHT-SIZING
//

func demoWorkerCounts(ctx context.Context, procs int) error {
	counts := []int{1, 2, procs / 2, procs, procs * 2, procs * 4, procs * 16}
	counts = dedupe(counts)

	section("1a. CPU-bound work: the answer is GOMAXPROCS")

	cpuJobs := makeJobs(240, 512)

	cpuMeasurements, err := pipeline.Sweep(ctx, cpuJobs, pipeline.CPUBound(400), counts)
	if err != nil {
		return err
	}

	printSweep(cpuMeasurements, procs)

	fmt.Println()
	fmt.Println("  throughput flattens at GOMAXPROCS and then stops improving: past that")
	fmt.Println("  point the goroutines are not running in parallel, they are taking")
	fmt.Println("  turns on the same cores. The extra ones only add scheduling overhead.")

	section("1b. I/O-bound work: the answer is much larger")

	ioJobs := makeJobs(240, 16)

	ioMeasurements, err := pipeline.Sweep(ctx, ioJobs, pipeline.IOBound(4*time.Millisecond), counts)
	if err != nil {
		return err
	}

	printSweep(ioMeasurements, procs)

	fmt.Println()
	fmt.Println("  a goroutine blocked on I/O holds no core, so throughput keeps climbing")
	fmt.Println("  far past GOMAXPROCS. The real ceiling is the thing you are calling -")
	fmt.Println("  its connection pool, its rate limit - which is why the pool is BOUNDED")
	fmt.Println("  rather than one goroutine per item.")

	return nil
}

func printSweep(measurements []pipeline.Measurement, procs int) {
	fmt.Printf("  %-10s %12s %14s %s\n", "workers", "duration", "jobs/sec", "")

	var baseline float64

	for i, measurement := range measurements {
		if i == 0 {
			baseline = measurement.Throughput
		}

		note := ""

		if measurement.Workers == procs {
			note = "  <- GOMAXPROCS"
		}

		fmt.Printf("  %-10d %12s %14.0f  %.1fx%s\n",
			measurement.Workers,
			measurement.Duration.Round(time.Millisecond),
			measurement.Throughput,
			measurement.Throughput/baseline,
			note)
	}
}

//
// 2. LEAKS
//

func demoLeaks() error {
	section("2. Goroutine leaks")

	before := leak.Count()

	fmt.Printf("  goroutines before: %d\n", before)

	// Leak 1: the send with no receiver. Each timed-out call strands a
	// goroutine on an unbuffered channel, forever.
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)

		if _, err := leak.LeakyRequest(ctx, 5*time.Second); err == nil {
			cancel()

			return errors.New("expected the leaky request to time out")
		}

		cancel()
	}

	leaked := leak.Count()

	fmt.Printf("  after 50 timed-out LeakyRequest calls: %d (+%d)\n", leaked, leaked-before)
	fmt.Println("  each one is blocked forever on 'result <- \"done\"' with no receiver,")
	fmt.Println("  holding its whole stack - and everything the stack references - alive")

	fmt.Println()
	fmt.Println("  the goroutine profile names the line:")

	stacks, err := leak.TopStacks(4)
	if err != nil {
		return err
	}

	for _, line := range strings.Split(stacks, "\n") {
		fmt.Println("    " + line)
	}

	// The fix: a buffer of one. The send always completes, so the goroutine
	// always returns.
	fixedBefore := leak.Count()

	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)

		if _, err := leak.FixedRequest(ctx, 20*time.Millisecond); err == nil {
			cancel()

			return errors.New("expected the fixed request to time out too")
		}

		cancel()
	}

	// Give the goroutines their 20ms of work plus scheduling slack.
	settled, ok := leak.Settle(fixedBefore, 3*time.Second)

	fmt.Println()
	fmt.Printf("  after 50 timed-out FixedRequest calls: %d (settled back: %t)\n", settled, ok)
	fmt.Println("  one character - make(chan string, 1) - and the goroutine can always")
	fmt.Println("  finish its send and return, whether anyone is listening or not")

	return nil
}

//
// 3. ERRGROUP
//

func demoErrgroup(ctx context.Context) error {
	section("3a. errgroup.SetLimit bounds the fan-out")

	var tracker fanout.MaxConcurrent

	tasks := make([]fanout.Task, 40)

	for i := range tasks {
		name := fmt.Sprintf("task-%02d", i)

		tasks[i] = fanout.Task{
			Name: name,
			Run: func(ctx context.Context) (string, error) {
				tracker.Enter()
				defer tracker.Leave()

				select {
				case <-time.After(5 * time.Millisecond):
					return name + ":ok", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			},
		}
	}

	results := fanout.NewResults()

	start := time.Now()

	if err := fanout.RunAll(ctx, tasks, 4, results); err != nil {
		return err
	}

	fmt.Printf("  40 tasks, SetLimit(4): peak concurrency %d, wall %s\n",
		tracker.Peak(), time.Since(start).Round(time.Millisecond))
	fmt.Println("  without a limit this would open 40 connections at once. Against a")
	fmt.Println("  database with a pool of 10, the outage lands over there, not here.")

	section("3b. The first error cancels the siblings")

	failure := errors.New("payment gateway rejected the request")

	mixed := []fanout.Task{
		fanout.Slow("inventory", 2*time.Second),
		fanout.Slow("shipping", 2*time.Second),
		fanout.Failing("payment", 20*time.Millisecond, failure),
		fanout.Slow("notifications", 2*time.Second),
	}

	cancelResults := fanout.NewResults()

	start = time.Now()

	err := fanout.RunAll(ctx, mixed, 0, cancelResults)

	elapsed := time.Since(start)

	started, finished, cancelled := cancelResults.Counts()

	fmt.Printf("  error: %v\n", err)
	fmt.Printf("  wrapped the original: %t\n", errors.Is(err, failure))
	fmt.Printf("  started=%d finished=%d cancelled-by-sibling=%d\n", started, finished, cancelled)
	fmt.Printf("  wall %s - not the 2s the slow tasks asked for\n", elapsed.Round(time.Millisecond))
	fmt.Println()
	fmt.Println("  errgroup.WithContext cancelled the other three the moment payment")
	fmt.Println("  failed. Nobody kept working for a result that was already lost - and")
	fmt.Println("  Wait returned the FIRST error, not a slice of all of them.")

	fmt.Printf("\n  goroutines at the end: %d\n", leak.Count())

	return nil
}

func makeJobs(count, size int) []pipeline.Job {
	jobs := make([]pipeline.Job, count)

	for i := range jobs {
		input := make([]byte, size)

		for j := range input {
			input[j] = byte(i + j)
		}

		jobs[i] = pipeline.Job{ID: i, Input: input}
	}

	return jobs
}

func dedupe(values []int) []int {
	seen := make(map[int]bool, len(values))
	out := make([]int, 0, len(values))

	for _, value := range values {
		if value < 1 || seen[value] {
			continue
		}

		seen[value] = true

		out = append(out, value)
	}

	return out
}

func section(title string) {
	underline := make([]byte, len(title))

	for i := range underline {
		underline[i] = '='
	}

	fmt.Printf("\n%s\n%s\n", title, underline)
}
