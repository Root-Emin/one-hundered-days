// Package fanout coordinates concurrent work with golang.org/x/sync/errgroup.
//
// errgroup is a WaitGroup that also carries the first error and cancels the
// rest. Written by hand, that is a WaitGroup, a buffered error channel, a
// context.WithCancel, and the discipline to get all three right every time.
//
// The three things it gives you:
//
//	Wait returns the FIRST non-nil error         (not the last, not a slice)
//	WithContext cancels siblings on that error   (nobody keeps working for a
//	                                              result that is already lost)
//	SetLimit bounds concurrency                   (a semaphore, without the
//	                                              channel bookkeeping)
package fanout

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Task is one piece of the fan-out.
type Task struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// Results collects what the tasks returned.
type Results struct {
	mu     sync.Mutex
	values map[string]string

	started   atomic.Int64
	finished  atomic.Int64
	cancelled atomic.Int64
}

func NewResults() *Results {
	return &Results{values: make(map[string]string)}
}

func (r *Results) set(name, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.values[name] = value
}

func (r *Results) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	names := make([]string, 0, len(r.values))

	for name := range r.values {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

func (r *Results) Get(name string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	value, found := r.values[name]

	return value, found
}

func (r *Results) Counts() (started, finished, cancelled int64) {
	return r.started.Load(), r.finished.Load(), r.cancelled.Load()
}

// RunAll executes every task concurrently and returns the first error.
//
// limit caps how many run at once; 0 means unlimited. Unlimited is almost
// never what you want against a shared dependency: 10,000 tasks means 10,000
// simultaneous connections, and the failure lands on the database rather than
// on you.
func RunAll(ctx context.Context, tasks []Task, limit int, results *Results) error {
	// WithContext gives every task a context that is cancelled as soon as any
	// task returns an error. That is the difference between "one failed" and
	// "one failed and the other nine are still burning CPU for nothing".
	group, groupCtx := errgroup.WithContext(ctx)

	if limit > 0 {
		group.SetLimit(limit)
	}

	for _, task := range tasks {
		group.Go(func() error {
			results.started.Add(1)

			value, err := task.Run(groupCtx)
			if err != nil {
				if groupCtx.Err() != nil && ctx.Err() == nil {
					// Cancelled because a SIBLING failed, not because this
					// task was broken. Worth counting separately: it is the
					// evidence that cancellation propagated.
					results.cancelled.Add(1)
				}

				return fmt.Errorf("task %s: %w", task.Name, err)
			}

			results.set(task.Name, value)
			results.finished.Add(1)

			return nil
		})
	}

	// Wait returns the first non-nil error. The others are discarded - if you
	// need them all, collect them yourself inside each task.
	return group.Wait()
}

// Slow is a task that takes its time and honours cancellation.
func Slow(name string, duration time.Duration) Task {
	return Task{
		Name: name,
		Run: func(ctx context.Context) (string, error) {
			timer := time.NewTimer(duration)
			defer timer.Stop()

			select {
			case <-timer.C:
				return name + ":ok", nil

			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
}

// Failing fails after a delay, so siblings are demonstrably still running when
// it does.
func Failing(name string, after time.Duration, err error) Task {
	return Task{
		Name: name,
		Run: func(ctx context.Context) (string, error) {
			timer := time.NewTimer(after)
			defer timer.Stop()

			select {
			case <-timer.C:
				return "", err

			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
}

// MaxConcurrent tracks the high-water mark of simultaneous runners, which is
// how a test proves SetLimit is actually limiting anything.
type MaxConcurrent struct {
	current atomic.Int64
	peak    atomic.Int64
}

func (m *MaxConcurrent) Enter() {
	current := m.current.Add(1)

	for {
		peak := m.peak.Load()

		if current <= peak || m.peak.CompareAndSwap(peak, current) {
			return
		}
	}
}

func (m *MaxConcurrent) Leave() {
	m.current.Add(-1)
}

func (m *MaxConcurrent) Peak() int64 {
	return m.peak.Load()
}
