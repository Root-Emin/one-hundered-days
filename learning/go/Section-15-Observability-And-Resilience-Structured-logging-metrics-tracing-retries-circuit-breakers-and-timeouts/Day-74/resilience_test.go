package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

//
// RETRY
//

func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	t.Parallel()

	var calls int

	policy := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

	err := Do(context.Background(), policy, func(ctx context.Context) error {
		calls++

		if calls < 3 {
			return RetryableError{Err: errors.New("temporary")}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryStopsAtMaxAttempts(t *testing.T) {
	t.Parallel()

	var calls int

	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}

	err := Do(context.Background(), policy, func(ctx context.Context) error {
		calls++

		return RetryableError{Err: errors.New("still broken")}
	})

	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}

	if calls != 3 {
		t.Fatalf("calls = %d, want exactly MaxAttempts", calls)
	}
}

// TestRetryDoesNotRetryPermanentErrors is the difference between a resilient
// client and a denial-of-service tool aimed at your own dependency.
func TestRetryDoesNotRetryPermanentErrors(t *testing.T) {
	t.Parallel()

	var calls int

	permanent := errors.New("bad request")

	err := Do(context.Background(), RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond},
		func(ctx context.Context) error {
			calls++

			return PermanentError{Err: permanent}
		})

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	if !errors.Is(err, permanent) {
		t.Fatalf("err = %v, want the original error unwrapped", err)
	}
}

func TestRetryRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	var calls atomic.Int64

	// Cancel while the retry loop is backing off.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := Do(ctx, RetryPolicy{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond},
		func(ctx context.Context) error {
			calls.Add(1)

			return RetryableError{Err: errors.New("temporary")}
		})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	if calls.Load() > 2 {
		t.Fatalf("calls = %d: the loop kept going after cancellation", calls.Load())
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 500 * time.Millisecond}

	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond, // capped
		500 * time.Millisecond,
	}

	for i, want := range expected {
		if got := policy.delayFor(i + 1); got != want {
			t.Errorf("attempt %d delay = %s, want %s", i+1, got, want)
		}
	}
}

func TestJitterStaysWithinTheBound(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second, Jitter: true}

	var distinct int

	previous := time.Duration(-1)

	for range 100 {
		delay := policy.delayFor(3) // no-jitter value would be 400ms

		if delay < 0 || delay > 400*time.Millisecond {
			t.Fatalf("jittered delay %s is outside [0, 400ms]", delay)
		}

		if delay != previous {
			distinct++
		}

		previous = delay
	}

	// Full jitter must actually vary, or it is not jitter.
	if distinct < 50 {
		t.Fatalf("only %d distinct delays in 100 samples", distinct)
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	retryable := []error{
		&HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503"},
		&HTTPStatusError{StatusCode: http.StatusTooManyRequests, Status: "429"},
		&HTTPStatusError{StatusCode: http.StatusGatewayTimeout, Status: "504"},
		errors.New("dial tcp: connection refused"),
		errors.New("read: connection reset by peer"),
		RetryableError{Err: errors.New("explicitly marked")},
	}

	for _, err := range retryable {
		if !IsRetryable(err) {
			t.Errorf("IsRetryable(%v) = false, want true", err)
		}
	}

	permanent := []error{
		nil,
		&HTTPStatusError{StatusCode: http.StatusBadRequest, Status: "400"},
		&HTTPStatusError{StatusCode: http.StatusNotFound, Status: "404"},
		&HTTPStatusError{StatusCode: http.StatusUnprocessableEntity, Status: "422"},
		context.Canceled,
		context.DeadlineExceeded,
		PermanentError{Err: errors.New("marked permanent")},
		errors.New("some unknown failure"),
	}

	for _, err := range permanent {
		if IsRetryable(err) {
			t.Errorf("IsRetryable(%v) = true, want false", err)
		}
	}
}

//
// BREAKER
//

// fakeClock lets the breaker's timing be tested without sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func newTestBreaker(clock *fakeClock, changes *[]string) *Breaker {
	return NewBreaker(BreakerConfig{
		Name:            "test",
		MinimumRequests: 4,
		FailureRatio:    0.5,
		Window:          time.Minute,
		Cooldown:        10 * time.Second,
		HalfOpenProbes:  1,
		Now:             clock.Now,
		OnStateChange: func(name string, from, to State) {
			if changes != nil {
				*changes = append(*changes, from.String()+"->"+to.String())
			}
		},
	})
}

func TestBreakerStaysClosedBelowTheMinimum(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}
	breaker := newTestBreaker(clock, nil)

	failing := func(ctx context.Context) error { return errors.New("boom") }

	// Three failures out of three is 100% - but below MinimumRequests, so the
	// breaker must not judge. This is what stops a quiet service from
	// tripping on its first failed request at 3am.
	for range 3 {
		if err := breaker.Do(context.Background(), failing); err == nil {
			t.Fatal("expected the failure to propagate")
		}
	}

	if breaker.State() != StateClosed {
		t.Fatalf("state = %s, want closed", breaker.State())
	}
}

func TestBreakerOpensAndFailsFast(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}

	var changes []string

	breaker := newTestBreaker(clock, &changes)

	failing := func(ctx context.Context) error { return errors.New("boom") }

	var calls int

	counted := func(ctx context.Context) error {
		calls++

		return failing(ctx)
	}

	for range 4 {
		_ = breaker.Do(context.Background(), counted)
	}

	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open", breaker.State())
	}

	// Now the dependency must not be called at all.
	err := breaker.Do(context.Background(), counted)

	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("err = %v, want ErrBreakerOpen", err)
	}

	if calls != 4 {
		t.Fatalf("calls = %d, want 4: the breaker let a call through while open", calls)
	}

	if len(changes) == 0 || changes[0] != "closed->open" {
		t.Fatalf("state changes = %v", changes)
	}
}

func TestBreakerHalfOpensAfterCooldown(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}

	var changes []string

	breaker := newTestBreaker(clock, &changes)

	for range 4 {
		_ = breaker.Do(context.Background(), func(ctx context.Context) error {
			return errors.New("boom")
		})
	}

	if breaker.State() != StateOpen {
		t.Fatal("breaker did not open")
	}

	// Not yet.
	clock.advance(9 * time.Second)

	if breaker.State() != StateOpen {
		t.Fatal("breaker half-opened before the cooldown elapsed")
	}

	clock.advance(2 * time.Second)

	if breaker.State() != StateHalfOpen {
		t.Fatalf("state = %s, want half-open", breaker.State())
	}

	// A successful probe closes it.
	if err := breaker.Do(context.Background(), func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("probe: %v", err)
	}

	if breaker.State() != StateClosed {
		t.Fatalf("state = %s, want closed after a successful probe", breaker.State())
	}
}

func TestFailedProbeReopensTheBreaker(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}
	breaker := newTestBreaker(clock, nil)

	for range 4 {
		_ = breaker.Do(context.Background(), func(ctx context.Context) error {
			return errors.New("boom")
		})
	}

	clock.advance(11 * time.Second)

	if breaker.State() != StateHalfOpen {
		t.Fatal("breaker did not half-open")
	}

	// The probe fails: back to open, without waiting for another window.
	_ = breaker.Do(context.Background(), func(ctx context.Context) error {
		return errors.New("still broken")
	})

	if breaker.State() != StateOpen {
		t.Fatalf("state = %s, want open again", breaker.State())
	}
}

// TestHalfOpenLimitsProbes: a recovering dependency must not be hit by every
// waiting caller at once.
func TestHalfOpenLimitsProbes(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}
	breaker := newTestBreaker(clock, nil)

	for range 4 {
		_ = breaker.Do(context.Background(), func(ctx context.Context) error {
			return errors.New("boom")
		})
	}

	clock.advance(11 * time.Second)

	var probes atomic.Int64

	slowProbe := func(ctx context.Context) error {
		probes.Add(1)

		time.Sleep(30 * time.Millisecond)

		return nil
	}

	var waitGroup sync.WaitGroup

	for range 8 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			_ = breaker.Do(context.Background(), slowProbe)
		}()
	}

	waitGroup.Wait()

	if probes.Load() != 1 {
		t.Fatalf("%d probes reached the dependency, want 1", probes.Load())
	}
}

// TestCancelledCallsDoNotCountAgainstTheDependency: a client that hangs up is
// not evidence that the dependency is unhealthy.
func TestCancelledCallsDoNotCountAgainstTheDependency(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}
	breaker := newTestBreaker(clock, nil)

	for range 10 {
		_ = breaker.Do(context.Background(), func(ctx context.Context) error {
			return context.Canceled
		})
	}

	if breaker.State() != StateClosed {
		t.Fatalf("state = %s: cancelled calls tripped the breaker", breaker.State())
	}

	successes, failures := breaker.Counts()

	if successes != 0 || failures != 0 {
		t.Fatalf("counts = %d/%d, want the cancelled calls to be ignored", successes, failures)
	}
}

func TestBreakerIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Now()}
	breaker := newTestBreaker(clock, nil)

	var waitGroup sync.WaitGroup

	for i := range 100 {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			_ = breaker.Do(context.Background(), func(ctx context.Context) error {
				if i%2 == 0 {
					return errors.New("boom")
				}

				return nil
			})

			breaker.State()
			breaker.Counts()
		}(i)
	}

	waitGroup.Wait()
}

//
// THE THREE COMBINED
//

func TestClientRetriesTransientServerErrors(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		if _, err := w.Write([]byte("ok")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))

	t.Cleanup(server.Close)

	client := NewResilientClient(server.URL, testLogger())

	body, err := client.Get(t.Context(), "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}

	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

// TestPostIsNotRetriedWithoutAnIdempotencyKey is the rule that prevents double
// charges.
func TestPostIsNotRetriedWithoutAnIdempotencyKey(t *testing.T) {
	t.Parallel()

	var withoutKey, withKey atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") == "" {
			withoutKey.Add(1)
		} else {
			withKey.Add(1)
		}

		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	t.Cleanup(server.Close)

	client := NewResilientClient(server.URL, testLogger())

	if _, err := client.Post(t.Context(), "/charges", "", nil); err == nil {
		t.Fatal("expected a failure")
	}

	if withoutKey.Load() != 1 {
		t.Fatalf("a POST without an idempotency key was attempted %d times", withoutKey.Load())
	}

	if _, err := client.Post(t.Context(), "/charges", "key-123", nil); err == nil {
		t.Fatal("expected a failure")
	}

	if withKey.Load() < 2 {
		t.Fatalf("a POST WITH an idempotency key was attempted %d times, want it retried", withKey.Load())
	}
}

// TestPerAttemptTimeoutBoundsOneTry: a hung server must not consume the whole
// retry budget in the first attempt.
func TestPerAttemptTimeoutBoundsOneTry(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))

	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := NewResilientClient(server.URL, testLogger())
	client.PerAttemptTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	start := time.Now()

	_, err := client.Get(ctx, "/")

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout")
	}

	// Three attempts of ~50ms plus backoff, nowhere near the 2s ceiling.
	if elapsed > time.Second {
		t.Fatalf("took %s: the per-attempt timeout did not bound the try", elapsed)
	}
}

func TestBreakerStopsCallingADeadDependency(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		w.WriteHeader(http.StatusInternalServerError)
	}))

	t.Cleanup(server.Close)

	client := NewResilientClient(server.URL, testLogger())

	var fastFailures int

	for range 10 {
		if _, err := client.Get(t.Context(), "/"); errors.Is(err, ErrBreakerOpen) {
			fastFailures++
		}
	}

	if fastFailures == 0 {
		t.Fatal("the breaker never opened")
	}

	if client.Breaker().State() != StateOpen {
		t.Fatalf("state = %s, want open", client.Breaker().State())
	}

	// Without the breaker this would be 30 calls (10 requests x 3 attempts).
	if calls.Load() >= 30 {
		t.Fatalf("dependency was called %d times: the breaker did not reduce load", calls.Load())
	}
}
