package pipeline_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/leak"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/pipeline"
)

func makeJobs(count int) []pipeline.Job {
	jobs := make([]pipeline.Job, count)

	for i := range jobs {
		jobs[i] = pipeline.Job{ID: i, Input: []byte{byte(i), byte(i + 1)}}
	}

	return jobs
}

func TestEveryJobIsProcessedExactlyOnce(t *testing.T) {
	jobs := makeJobs(200)

	pool := pipeline.New(8, pipeline.CPUBound(2))

	results := pool.Run(t.Context(), jobs)

	if len(results) != len(jobs) {
		t.Fatalf("results = %d, want %d", len(results), len(jobs))
	}

	seen := make(map[int]int, len(jobs))

	for _, result := range results {
		seen[result.JobID]++
	}

	for _, job := range jobs {
		if seen[job.ID] != 1 {
			t.Errorf("job %d processed %d times, want 1", job.ID, seen[job.ID])
		}
	}

	processed, failed := pool.Stats()

	if processed != int64(len(jobs)) || failed != 0 {
		t.Errorf("stats = (%d, %d), want (%d, 0)", processed, failed, len(jobs))
	}
}

// The work has to actually be shared - a "pool" where one worker does
// everything is just a slower single goroutine.
func TestWorkIsSpreadAcrossWorkers(t *testing.T) {
	jobs := makeJobs(400)

	pool := pipeline.New(4, pipeline.CPUBound(50))

	used := make(map[int]bool)

	for _, result := range pool.Run(t.Context(), jobs) {
		used[result.Worker] = true
	}

	if len(used) < 2 {
		t.Errorf("only %d worker(s) did anything, want the work spread", len(used))
	}
}

// The pool must not outlive its own Run call, whatever the outcome.
func TestPoolLeavesNoGoroutinesBehind(t *testing.T) {
	before := leak.Count()

	pool := pipeline.New(16, pipeline.IOBound(time.Millisecond))

	pool.Run(t.Context(), makeJobs(64))

	if got, settled := leak.Settle(before, 3*time.Second); !settled {
		t.Errorf("goroutines did not settle back: %d, want %d", got, before)
	}
}

// Cancellation has to reach the workers, the producer AND the collector.
func TestCancellationStopsEverything(t *testing.T) {
	before := leak.Count()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	pool := pipeline.New(4, pipeline.IOBound(500*time.Millisecond))

	start := time.Now()

	results := pool.Run(ctx, makeJobs(200))

	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("Run took %s after a 20ms timeout - cancellation did not propagate", elapsed)
	}

	if len(results) >= 200 {
		t.Errorf("got %d results despite cancellation", len(results))
	}

	if got, settled := leak.Settle(before, 3*time.Second); !settled {
		t.Errorf("goroutines did not settle back after cancellation: %d, want %d", got, before)
	}
}

func TestHandlerErrorsAreReported(t *testing.T) {
	pool := pipeline.New(4, pipeline.Failing(pipeline.CPUBound(1), 3))

	results := pool.Run(t.Context(), makeJobs(30))

	failures := 0

	for _, result := range results {
		if result.Err != nil {
			failures++

			if !errors.Is(result.Err, pipeline.ErrInjected) {
				t.Errorf("job %d: error = %v, want ErrInjected", result.JobID, result.Err)
			}
		}
	}

	if failures != 10 {
		t.Errorf("failures = %d, want 10", failures)
	}

	processed, failed := pool.Stats()

	if failed != 10 || processed != 20 {
		t.Errorf("stats = (%d processed, %d failed), want (20, 10)", processed, failed)
	}
}

// The claim from the demo, as an assertion: I/O-bound work gets meaningfully
// faster past GOMAXPROCS, where CPU-bound work does not.
func TestIOBoundWorkScalesPastGOMAXPROCS(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}

	procs := runtime.GOMAXPROCS(0)
	jobs := makeJobs(64)

	measurements, err := pipeline.Sweep(t.Context(), jobs, pipeline.IOBound(5*time.Millisecond),
		[]int{procs, procs * 4})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	atProcs, beyond := measurements[0].Throughput, measurements[1].Throughput

	// Deliberately loose: the claim is "meaningfully more", not a precise
	// multiple, and a tight bound here would be a flaky test.
	if beyond < atProcs*1.5 {
		t.Errorf("throughput at %dx workers = %.0f, at GOMAXPROCS = %.0f: expected I/O-bound work to scale",
			4, beyond, atProcs)
	}
}

func TestSweepReturnsOneMeasurementPerWorkerCount(t *testing.T) {
	measurements, err := pipeline.Sweep(t.Context(), makeJobs(20), pipeline.CPUBound(1), []int{1, 2, 4})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(measurements) != 3 {
		t.Fatalf("measurements = %d, want 3", len(measurements))
	}

	for _, measurement := range measurements {
		if measurement.Throughput <= 0 {
			t.Errorf("workers=%d: throughput = %f", measurement.Workers, measurement.Throughput)
		}
	}
}
