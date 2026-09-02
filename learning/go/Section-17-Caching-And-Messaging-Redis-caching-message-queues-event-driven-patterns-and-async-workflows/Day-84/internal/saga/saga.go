// Package saga coordinates a multi-step workflow across services.
//
// The problem: "place an order" spans payment, inventory and shipping, in
// three different services with three different databases. There is no
// distributed transaction to roll them back together.
//
// A saga replaces the rollback with COMPENSATION: each step has an inverse,
// and when step three fails, the saga runs the inverses of two and one, in
// reverse order. The system reaches a consistent state - not by undoing time,
// but by doing the opposite thing.
//
// This is an ORCHESTRATED saga: one coordinator drives the steps. The
// alternative, CHOREOGRAPHY, has each service react to the previous one's
// event - less coupling, and much harder to see the workflow as a whole.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StatePending      State = "pending"
	StateRunning      State = "running"
	StateCompleted    State = "completed"
	StateCompensating State = "compensating"
	StateCompensated  State = "compensated"
	// StateFailed is the state that needs a human: the work failed AND the
	// compensation failed, so the system is inconsistent.
	StateFailed State = "failed"
)

// Step is one unit of work plus its inverse.
type Step struct {
	Name string

	// Execute does the work. It must be idempotent: a saga can be resumed
	// after a crash, and the step may run again.
	Execute func(ctx context.Context, data *Data) error

	// Compensate undoes it. It must ALSO be idempotent, and it must tolerate
	// being called for a step that never completed - after a crash the
	// coordinator cannot always tell.
	Compensate func(ctx context.Context, data *Data) error
}

// Data is the saga's shared state, passed from step to step.
type Data struct {
	mu     sync.Mutex
	values map[string]any
}

func NewData() *Data {
	return &Data{values: make(map[string]any)}
}

func (d *Data) Set(key string, value any) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.values[key] = value
}

func (d *Data) Get(key string) (any, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	value, found := d.values[key]

	return value, found
}

// Log records what happened, which is what makes a saga debuggable.
type Entry struct {
	Step     string
	Action   string
	Err      error
	At       time.Time
	Duration time.Duration
}

type Saga struct {
	name   string
	steps  []Step
	logger *slog.Logger

	mu      sync.Mutex
	state   State
	history []Entry
}

func New(name string, logger *slog.Logger, steps ...Step) *Saga {
	if logger == nil {
		logger = slog.Default()
	}

	return &Saga{name: name, steps: steps, logger: logger, state: StatePending}
}

func (s *Saga) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.state
}

func (s *Saga) History() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Entry(nil), s.history...)
}

func (s *Saga) record(entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, entry)
}

func (s *Saga) setState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = state
}

// ErrCompensated means the saga rolled back cleanly: the work did not happen,
// and the system is consistent.
var ErrCompensated = errors.New("saga compensated")

// Run executes the steps in order, compensating in reverse on failure.
func (s *Saga) Run(ctx context.Context, data *Data) error {
	s.setState(StateRunning)

	completed := make([]int, 0, len(s.steps))

	for index, step := range s.steps {
		start := time.Now()

		err := step.Execute(ctx, data)

		s.record(Entry{Step: step.Name, Action: "execute", Err: err, At: start, Duration: time.Since(start)})

		if err == nil {
			completed = append(completed, index)

			s.logger.Info("saga step completed",
				slog.String("saga", s.name), slog.String("step", step.Name))

			continue
		}

		s.logger.Warn("saga step failed, compensating",
			slog.String("saga", s.name),
			slog.String("step", step.Name),
			slog.String("error", err.Error()))

		s.setState(StateCompensating)

		// Compensate in REVERSE order: the last thing done is the first thing
		// undone, because later steps may depend on earlier ones.
		compensationErr := s.compensate(ctx, data, completed)

		if compensationErr != nil {
			// The work failed and the undo failed: the system is now
			// inconsistent, and no amount of retrying inside this process
			// will fix it. This is the state that pages a human.
			s.setState(StateFailed)

			return fmt.Errorf("step %s failed (%w) and compensation failed: %w",
				step.Name, err, compensationErr)
		}

		s.setState(StateCompensated)

		return fmt.Errorf("%w: step %s failed: %w", ErrCompensated, step.Name, err)
	}

	s.setState(StateCompleted)

	return nil
}

func (s *Saga) compensate(ctx context.Context, data *Data, completed []int) error {
	var failures []error

	for i := len(completed) - 1; i >= 0; i-- {
		step := s.steps[completed[i]]

		if step.Compensate == nil {
			// A step with no inverse is a step that cannot be undone - a sent
			// email, a charged card without a refund path. Knowing which
			// steps those are is part of designing the saga.
			continue
		}

		start := time.Now()

		// Compensation runs on a FRESH context: the original may already be
		// cancelled (that may be why the step failed), and an undo must not
		// be skipped because the caller gave up.
		compensateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)

		err := step.Compensate(compensateCtx, data)

		cancel()

		s.record(Entry{Step: step.Name, Action: "compensate", Err: err, At: start, Duration: time.Since(start)})

		if err != nil {
			failures = append(failures, fmt.Errorf("compensate %s: %w", step.Name, err))

			s.logger.Error("compensation failed",
				slog.String("saga", s.name),
				slog.String("step", step.Name),
				slog.String("error", err.Error()))

			// Keep going: undoing the remaining steps is still better than
			// leaving all of them half-done.
			continue
		}

		s.logger.Info("saga step compensated",
			slog.String("saga", s.name), slog.String("step", step.Name))
	}

	return errors.Join(failures...)
}

// Describe renders the saga for documentation.
func (s *Saga) Describe() string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("saga %q\n", s.name))

	for i, step := range s.steps {
		inverse := "(no compensation - this step cannot be undone)"

		if step.Compensate != nil {
			inverse = "compensated by its inverse"
		}

		builder.WriteString(fmt.Sprintf("  %d. %-20s %s\n", i+1, step.Name, inverse))
	}

	return builder.String()
}
