// Package resilience is the retry and circuit-breaker implementation from
// Day 74, packaged for reuse by this capstone.
//
// Unchanged from Day 74 except for the package name: the point of the day is
// to WIRE these together with telemetry, not to rewrite them.
package resilience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"
)

/*
Retries.

Three rules, in order of importance:

 1. Only retry what is safe to repeat. A GET is safe. A POST that charges a
    card is not, unless it carries an idempotency key.
 2. Only retry what might succeed next time. A 400 will be a 400 forever;
    retrying it wastes the caller's deadline and the server's capacity.
 3. Back off, and add jitter. Without backoff, a struggling dependency gets
    hammered exactly when it is weakest; without jitter, every client that
    failed at the same moment retries at the same moment.
*/

// RetryPolicy describes how hard to try.
type RetryPolicy struct {
	// MaxAttempts includes the first try. 3 means one call and two retries.
	MaxAttempts int

	// BaseDelay is the first backoff step; each retry doubles it.
	BaseDelay time.Duration

	// MaxDelay caps the exponential growth, so attempt 10 does not sleep for
	// seventeen minutes.
	MaxDelay time.Duration

	// Jitter spreads retries out. "Full" jitter (a uniform random value
	// between 0 and the computed delay) is the variant AWS recommends: it
	// beats "equal" and "decorrelated" jitter for avoiding synchronised
	// retry storms.
	Jitter bool

	// Retryable decides whether an error is worth another attempt. Defaults
	// to IsRetryable.
	Retryable func(error) bool

	// OnRetry is called before each wait, for logging and metrics.
	OnRetry func(attempt int, delay time.Duration, err error)
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		Jitter:      true,
	}
}

// ErrExhausted wraps the final failure after every attempt was used.
var ErrExhausted = errors.New("retries exhausted")

// Do runs fn until it succeeds, the error is not retryable, the attempts run
// out, or the context is done.
//
// The context is checked BEFORE each attempt and during each wait: a caller
// whose deadline has passed must not be made to wait for one more try.
func Do(ctx context.Context, policy RetryPolicy, fn func(ctx context.Context) error) error {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}

	if policy.Retryable == nil {
		policy.Retryable = IsRetryable
	}

	var lastErr error

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("%w after %d attempt(s): %w", err, attempt-1, lastErr)
			}

			return err
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// A non-retryable error is returned immediately and unwrapped, so the
		// caller sees exactly what the dependency said.
		if !policy.Retryable(err) {
			return err
		}

		if attempt == policy.MaxAttempts {
			break
		}

		delay := policy.delayFor(attempt)

		if policy.OnRetry != nil {
			policy.OnRetry(attempt, delay, err)
		}

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("%w during backoff after %d attempt(s): %w", ctx.Err(), attempt, lastErr)

		case <-timer.C:
		}
	}

	return fmt.Errorf("%w after %d attempts: %w", ErrExhausted, policy.MaxAttempts, lastErr)
}

// delayFor computes the backoff for the given attempt (1-based).
func (p RetryPolicy) delayFor(attempt int) time.Duration {
	base := p.BaseDelay

	if base <= 0 {
		base = 50 * time.Millisecond
	}

	maximum := p.MaxDelay

	if maximum <= 0 {
		maximum = 30 * time.Second
	}

	// 2^(attempt-1) * base, capped. math.Pow on a float avoids the overflow a
	// shift would produce for a large attempt count.
	delay := time.Duration(math.Min(
		float64(base)*math.Pow(2, float64(attempt-1)),
		float64(maximum),
	))

	if !p.Jitter {
		return delay
	}

	// Full jitter: uniform in [0, delay]. The randomness only needs to be
	// unpredictable to other clients, not to an attacker, so math/rand is the
	// right tool.
	//nolint:gosec // not a security decision
	return time.Duration(rand.Int64N(int64(delay) + 1))
}

//
// CLASSIFICATION
//

// RetryableError marks an error the caller has decided is worth retrying.
type RetryableError struct {
	Err error
}

func (e RetryableError) Error() string { return e.Err.Error() }

func (e RetryableError) Unwrap() error { return e.Err }

// PermanentError marks an error that must not be retried, whatever it looks
// like. Wrapping is how a caller overrides the default classification.
type PermanentError struct {
	Err error
}

func (e PermanentError) Error() string { return e.Err.Error() }

func (e PermanentError) Unwrap() error { return e.Err }

// IsRetryable is the default classifier.
//
// Getting this wrong in either direction is expensive: retrying a permanent
// failure burns the deadline, and not retrying a transient one turns a blip
// into an outage.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	var permanent PermanentError
	if errors.As(err, &permanent) {
		return false
	}

	var retryable RetryableError
	if errors.As(err, &retryable) {
		return true
	}

	// A cancelled or expired context is the caller giving up: another attempt
	// cannot help.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors that report themselves as temporary or as timeouts.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		return retryableStatus(statusErr.StatusCode)
	}

	// Connection-level failures: the request may never have reached the
	// server, so a retry is usually safe and usually helps.
	message := strings.ToLower(err.Error())

	for _, marker := range []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"no such host",
		"eof",
		"server closed",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}

	return false
}

// retryableStatus encodes which HTTP responses are worth another attempt.
func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, // 408: the request timed out, try again
		http.StatusTooManyRequests,     // 429: back off and retry (honour Retry-After)
		http.StatusInternalServerError, // 500: possibly transient
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true

	default:
		// Everything else, 4xx in particular, will fail the same way forever.
		return false
	}
}

// HTTPStatusError carries a status code so the classifier can reason about it.
type HTTPStatusError struct {
	StatusCode int
	Status     string
	Body       string

	// RetryAfter is the server's own instruction, which always wins over the
	// client's backoff calculation.
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http %s", e.Status)
}
