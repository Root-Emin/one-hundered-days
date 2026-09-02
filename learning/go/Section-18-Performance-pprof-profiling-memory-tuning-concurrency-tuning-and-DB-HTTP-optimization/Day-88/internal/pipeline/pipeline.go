// Package pipeline is a worker pool you can resize, so the question "how many
// goroutines?" can be answered with a measurement instead of a guess.
//
// The answer depends entirely on what the work waits for:
//
//	CPU-bound   throughput plateaus at GOMAXPROCS. Beyond that the goroutines
//	            are not running in parallel - they are taking turns on the same
//	            cores, and the extra context switches cost more than nothing.
//	I/O-bound   a goroutine waiting on a socket occupies no core, so throughput
//	            keeps climbing well past GOMAXPROCS. The ceiling is set by the
//	            thing you are calling - its connection pool, its rate limit -
//	            not by your CPU count.
//
// Which is why "spawn a goroutine per item" is not a strategy: it is unbounded
// concurrency against a bounded dependency, and it fails as an outage in the
// system you are calling rather than in yours.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Job is one unit of work.
type Job struct {
	ID    int
	Input []byte
}

// Result carries the outcome back, including which worker handled it - that is
// how a test can prove the work really was spread across the pool.
type Result struct {
	JobID  int
	Worker int
	Output uint64
	Err    error
}

// Handler processes one job. It takes a context because a worker that ignores
// cancellation is a worker that keeps a shutdown waiting.
type Handler func(ctx context.Context, job Job) (uint64, error)

type Pool struct {
	workers int
	handler Handler

	processed atomic.Int64
	failed    atomic.Int64
}

func New(workers int, handler Handler) *Pool {
	if workers < 1 {
		workers = 1
	}

	return &Pool{workers: workers, handler: handler}
}

func (p *Pool) Stats() (processed, failed int64) {
	return p.processed.Load(), p.failed.Load()
}

// Run fans jobs out to a fixed set of workers and fans the results back in.
//
// The shape to notice:
//
//   - the jobs channel is closed by the producer, which is how workers learn
//     there is no more work. A worker range-ing over a channel nobody closes
//     is the most common goroutine leak there is.
//   - every send and receive also selects on ctx.Done(), so cancellation
//     unblocks a worker that is stuck trying to hand back a result nobody is
//     reading.
//   - results is closed by whoever owns the WaitGroup, after every worker has
//     returned. Closing it from a worker would panic the moment a second
//     worker tried to send.
func (p *Pool) Run(ctx context.Context, jobs []Job) []Result {
	jobCh := make(chan Job)
	resultCh := make(chan Result, len(jobs))

	var wg sync.WaitGroup

	for worker := 0; worker < p.workers; worker++ {
		wg.Add(1)

		go func(worker int) {
			defer wg.Done()

			for job := range jobCh {
				output, err := p.handler(ctx, job)

				if err != nil {
					p.failed.Add(1)
				} else {
					p.processed.Add(1)
				}

				select {
				case resultCh <- Result{JobID: job.ID, Worker: worker, Output: output, Err: err}:
				case <-ctx.Done():
					return
				}
			}
		}(worker)
	}

	// The producer runs in its own goroutine so Run can start draining results
	// immediately; with an unbuffered jobs channel, doing this inline would
	// deadlock as soon as the result buffer filled.
	go func() {
		defer close(jobCh)

		for _, job := range jobs {
			select {
			case jobCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]Result, 0, len(jobs))

	for result := range resultCh {
		results = append(results, result)
	}

	return results
}

//
// TWO WORKLOADS WITH OPPOSITE ANSWERS
//

// CPUBound burns actual CPU: an FNV-style hash over the input, repeated.
//
// No sleeping, no waiting - the goroutine holds a core for its whole duration,
// so more goroutines than cores cannot help.
func CPUBound(rounds int) Handler {
	return func(ctx context.Context, job Job) (uint64, error) {
		var hash uint64 = 14695981039346656037

		for round := 0; round < rounds; round++ {
			// Checking cancellation once per round keeps a long job
			// interruptible without checking on every byte.
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}

			for _, b := range job.Input {
				hash ^= uint64(b)
				hash *= 1099511628211
			}
		}

		return hash, nil
	}
}

// IOBound stands in for a database query or an HTTP call: the goroutine parks,
// the core is free, and the scheduler runs someone else.
func IOBound(latency time.Duration) Handler {
	return func(ctx context.Context, job Job) (uint64, error) {
		timer := time.NewTimer(latency)
		defer timer.Stop()

		select {
		case <-timer.C:
			return uint64(job.ID), nil

		case <-ctx.Done():
			// Returning the context error rather than a zero value means the
			// caller can tell "cancelled" from "finished with nothing".
			return 0, fmt.Errorf("job %d: %w", job.ID, ctx.Err())
		}
	}
}

// Failing fails every nth job, for testing error paths.
func Failing(handler Handler, everyN int) Handler {
	return func(ctx context.Context, job Job) (uint64, error) {
		if everyN > 0 && job.ID%everyN == 0 {
			return 0, fmt.Errorf("job %d: %w", job.ID, ErrInjected)
		}

		return handler(ctx, job)
	}
}

var ErrInjected = errors.New("injected failure")

// Measurement is one row of a worker-count sweep.
type Measurement struct {
	Workers    int
	Duration   time.Duration
	Throughput float64 // jobs per second
}

// Sweep runs the same jobs at several worker counts.
//
// This is the whole technique: do not reason about the right number, measure
// it on the machine that will run the code, with the work that will run on it.
func Sweep(ctx context.Context, jobs []Job, handler Handler, workerCounts []int) ([]Measurement, error) {
	measurements := make([]Measurement, 0, len(workerCounts))

	for _, workers := range workerCounts {
		pool := New(workers, handler)

		start := time.Now()

		results := pool.Run(ctx, jobs)

		elapsed := time.Since(start)

		if len(results) != len(jobs) {
			return nil, fmt.Errorf("workers=%d: got %d results for %d jobs", workers, len(results), len(jobs))
		}

		measurements = append(measurements, Measurement{
			Workers:    workers,
			Duration:   elapsed,
			Throughput: float64(len(jobs)) / elapsed.Seconds(),
		})
	}

	return measurements, nil
}
