package fanout_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/fanout"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/leak"
)

func TestAllTasksRunAndReturnValues(t *testing.T) {
	results := fanout.NewResults()

	tasks := []fanout.Task{
		fanout.Slow("a", time.Millisecond),
		fanout.Slow("b", time.Millisecond),
		fanout.Slow("c", time.Millisecond),
	}

	if err := fanout.RunAll(t.Context(), tasks, 0, results); err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	for _, name := range []string{"a", "b", "c"} {
		value, found := results.Get(name)
		if !found {
			t.Errorf("no result for %s", name)
		}

		if value != name+":ok" {
			t.Errorf("%s = %q", name, value)
		}
	}
}

// SetLimit is the difference between "concurrent" and "unbounded".
func TestSetLimitCapsConcurrency(t *testing.T) {
	var tracker fanout.MaxConcurrent

	tasks := make([]fanout.Task, 30)

	for i := range tasks {
		tasks[i] = fanout.Task{
			Name: string(rune('a' + i%26)),
			Run: func(ctx context.Context) (string, error) {
				tracker.Enter()
				defer tracker.Leave()

				select {
				case <-time.After(2 * time.Millisecond):
					return "ok", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			},
		}
	}

	if err := fanout.RunAll(t.Context(), tasks, 3, fanout.NewResults()); err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	if peak := tracker.Peak(); peak > 3 {
		t.Errorf("peak concurrency = %d, want at most 3", peak)
	}
}

// Wait returns the FIRST error, and WithContext cancels the siblings so nobody
// keeps working for a result that is already lost.
func TestFirstErrorCancelsSiblings(t *testing.T) {
	failure := errors.New("gateway rejected")

	results := fanout.NewResults()

	tasks := []fanout.Task{
		fanout.Slow("slow-1", 10*time.Second),
		fanout.Failing("payment", 5*time.Millisecond, failure),
		fanout.Slow("slow-2", 10*time.Second),
	}

	start := time.Now()

	err := fanout.RunAll(t.Context(), tasks, 0, results)

	elapsed := time.Since(start)

	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap the task failure", err)
	}

	if elapsed > 2*time.Second {
		t.Errorf("RunAll took %s - the siblings were not cancelled", elapsed)
	}

	started, finished, cancelled := results.Counts()

	if started != 3 {
		t.Errorf("started = %d, want 3", started)
	}

	if finished != 0 {
		t.Errorf("finished = %d, want 0", finished)
	}

	if cancelled != 2 {
		t.Errorf("cancelled-by-sibling = %d, want 2", cancelled)
	}
}

// A caller cancelling from outside must also stop everything - and must not be
// mistaken for a sibling failure.
func TestCallerCancellationStopsTheGroup(t *testing.T) {
	results := fanout.NewResults()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	tasks := []fanout.Task{
		fanout.Slow("a", 5*time.Second),
		fanout.Slow("b", 5*time.Second),
	}

	err := fanout.RunAll(ctx, tasks, 0, results)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}

	if _, _, cancelled := results.Counts(); cancelled != 0 {
		t.Errorf("cancelled-by-sibling = %d, want 0 (the caller cancelled, not a sibling)", cancelled)
	}
}

func TestGroupLeavesNoGoroutinesBehind(t *testing.T) {
	before := leak.Count()

	tasks := []fanout.Task{
		fanout.Slow("a", time.Millisecond),
		fanout.Failing("b", time.Millisecond, errors.New("nope")),
		fanout.Slow("c", 5*time.Second),
	}

	_ = fanout.RunAll(t.Context(), tasks, 0, fanout.NewResults())

	if got, settled := leak.Settle(before, 3*time.Second); !settled {
		t.Errorf("goroutines did not settle back: %d, want %d", got, before)
	}
}

func TestMaxConcurrentTracksThePeak(t *testing.T) {
	var tracker fanout.MaxConcurrent

	var wg atomic.Int64

	done := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func() {
			tracker.Enter()

			<-done

			tracker.Leave()
			wg.Add(-1)
		}()
	}

	// Wait until all five are inside.
	deadline := time.Now().Add(2 * time.Second)

	for tracker.Peak() < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("peak = %d, want 5", tracker.Peak())
		}

		time.Sleep(time.Millisecond)
	}

	close(done)

	for wg.Load() > 0 {
		time.Sleep(time.Millisecond)
	}

	if tracker.Peak() != 5 {
		t.Errorf("peak = %d, want 5", tracker.Peak())
	}
}
