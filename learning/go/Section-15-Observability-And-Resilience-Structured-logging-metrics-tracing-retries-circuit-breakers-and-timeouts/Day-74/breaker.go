package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

/*
A circuit breaker.

The problem it solves: when a dependency is down, retrying makes everything
worse. Every caller holds a goroutine and a connection waiting for a timeout,
the dependency gets no room to recover, and the queue of waiting requests takes
the CALLER down too. That is a cascading failure.

The breaker's answer is to stop calling for a while:

	CLOSED     normal. Failures are counted in a rolling window.
	  |        too many failures -> OPEN
	  v
	OPEN       every call fails instantly, without touching the dependency.
	  |        after the cooldown -> HALF-OPEN
	  v
	HALF-OPEN  a few probe calls are allowed through.
	           they succeed -> CLOSED. one fails -> OPEN again.

Production alternatives: sony/gobreaker (small, battle-tested) and
failsafe-go. This implementation exists so the state machine is visible;
the trade-offs it documents apply to those libraries too.
*/

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrBreakerOpen is returned instead of calling the dependency.
//
// Failing in microseconds is the entire value: the caller can fall back,
// serve stale data, or shed load, instead of blocking for a timeout.
var ErrBreakerOpen = errors.New("circuit breaker is open")

type BreakerConfig struct {
	// Name appears in logs and metrics.
	Name string

	// MinimumRequests is the smallest sample the breaker will judge. Without
	// it, one failed request out of one trips the breaker at 3am when traffic
	// is thin.
	MinimumRequests int

	// FailureRatio between 0 and 1: the share of failures in the window that
	// opens the breaker.
	FailureRatio float64

	// Window is the rolling period failures are counted over.
	Window time.Duration

	// Cooldown is how long the breaker stays open before probing.
	Cooldown time.Duration

	// HalfOpenProbes is how many calls are allowed through while half-open.
	// One is usually right: a struggling dependency should not be probed by
	// every caller at once.
	HalfOpenProbes int

	// Now is injectable so tests do not sleep.
	Now func() time.Time

	// OnStateChange is where logging and metrics belong. A breaker that opens
	// silently is a mystery outage.
	OnStateChange func(name string, from, to State)
}

func DefaultBreakerConfig(name string) BreakerConfig {
	return BreakerConfig{
		Name:            name,
		MinimumRequests: 10,
		FailureRatio:    0.5,
		Window:          10 * time.Second,
		Cooldown:        5 * time.Second,
		HalfOpenProbes:  1,
	}
}

type Breaker struct {
	config BreakerConfig

	mu           sync.Mutex
	state        State
	openedAt     time.Time
	probesIssued int
	probesPassed int

	// outcomes is the rolling window: one entry per call, trimmed by age.
	outcomes []outcome
}

type outcome struct {
	at      time.Time
	success bool
}

func NewBreaker(config BreakerConfig) *Breaker {
	if config.Now == nil {
		config.Now = time.Now
	}

	if config.MinimumRequests <= 0 {
		config.MinimumRequests = 10
	}

	if config.FailureRatio <= 0 || config.FailureRatio > 1 {
		config.FailureRatio = 0.5
	}

	if config.Window <= 0 {
		config.Window = 10 * time.Second
	}

	if config.Cooldown <= 0 {
		config.Cooldown = 5 * time.Second
	}

	if config.HalfOpenProbes <= 0 {
		config.HalfOpenProbes = 1
	}

	return &Breaker{config: config, state: StateClosed}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refreshState()

	return b.state
}

// Do runs fn unless the breaker is open.
func (b *Breaker) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := b.allow(); err != nil {
		return err
	}

	err := fn(ctx)

	// A cancelled caller is not evidence about the dependency's health, so it
	// must not count towards opening the breaker. Getting this wrong makes a
	// breaker trip during a deploy, when clients disconnect en masse.
	if errors.Is(err, context.Canceled) {
		b.release()

		return err
	}

	b.record(err == nil)

	return err
}

func (b *Breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refreshState()

	switch b.state {
	case StateOpen:
		return fmt.Errorf("%s: %w", b.config.Name, ErrBreakerOpen)

	case StateHalfOpen:
		if b.probesIssued >= b.config.HalfOpenProbes {
			// The probe budget is spent; everyone else still fails fast.
			return fmt.Errorf("%s: %w (probing)", b.config.Name, ErrBreakerOpen)
		}

		b.probesIssued++

		return nil

	default:
		return nil
	}
}

// release returns an unused probe slot, for calls that were abandoned rather
// than answered.
func (b *Breaker) release() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == StateHalfOpen && b.probesIssued > 0 {
		b.probesIssued--
	}
}

func (b *Breaker) record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.config.Now()

	b.outcomes = append(b.outcomes, outcome{at: now, success: success})
	b.trim(now)

	switch b.state {
	case StateHalfOpen:
		if !success {
			// One failed probe is enough: the dependency is still unwell.
			b.transition(StateOpen)

			return
		}

		b.probesPassed++

		if b.probesPassed >= b.config.HalfOpenProbes {
			b.transition(StateClosed)
		}

	case StateClosed:
		total := len(b.outcomes)

		if total < b.config.MinimumRequests {
			return
		}

		failures := 0

		for _, entry := range b.outcomes {
			if !entry.success {
				failures++
			}
		}

		if float64(failures)/float64(total) >= b.config.FailureRatio {
			b.transition(StateOpen)
		}
	}
}

// refreshState moves OPEN -> HALF-OPEN once the cooldown has passed. It must
// be called with the lock held.
func (b *Breaker) refreshState() {
	if b.state != StateOpen {
		return
	}

	if b.config.Now().Sub(b.openedAt) >= b.config.Cooldown {
		b.transition(StateHalfOpen)
	}
}

// transition must be called with the lock held.
func (b *Breaker) transition(to State) {
	if b.state == to {
		return
	}

	from := b.state
	b.state = to

	switch to {
	case StateOpen:
		b.openedAt = b.config.Now()
		b.probesIssued = 0
		b.probesPassed = 0

	case StateHalfOpen:
		b.probesIssued = 0
		b.probesPassed = 0

	case StateClosed:
		// Start the window fresh, or the failures that opened the breaker
		// immediately re-open it.
		b.outcomes = nil
		b.probesIssued = 0
		b.probesPassed = 0
	}

	if b.config.OnStateChange != nil {
		// Called with the lock held: the callback must not call back into the
		// breaker. Logging and metrics are fine; anything else is a deadlock.
		b.config.OnStateChange(b.config.Name, from, to)
	}
}

func (b *Breaker) trim(now time.Time) {
	cutoff := now.Add(-b.config.Window)

	kept := b.outcomes[:0]

	for _, entry := range b.outcomes {
		if entry.at.After(cutoff) {
			kept = append(kept, entry)
		}
	}

	b.outcomes = kept
}

// Counts reports the current window, for metrics and for tests.
func (b *Breaker) Counts() (successes, failures int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.trim(b.config.Now())

	for _, entry := range b.outcomes {
		if entry.success {
			successes++
		} else {
			failures++
		}
	}

	return successes, failures
}
