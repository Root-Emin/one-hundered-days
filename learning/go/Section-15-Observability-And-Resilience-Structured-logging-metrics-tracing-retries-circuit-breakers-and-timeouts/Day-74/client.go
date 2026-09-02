package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

/*
The three patterns, composed.

Order matters, from the outside in:

	breaker( retry( timeout( call ) ) )

  - the TIMEOUT is innermost: it bounds one attempt, so a hung dependency
    cannot consume the whole retry budget in the first try
  - the RETRY sits above it: it turns transient failures into successes
  - the BREAKER is outermost: it must see the final outcome of the whole
    retried operation, and it must be able to skip the retries entirely when
    the dependency is known to be down

Putting the breaker inside the retry is a classic mistake: the retry loop then
hammers an open breaker and reports "retries exhausted" instead of failing
fast.
*/

// ResilientClient wraps an HTTP client with timeouts, retries and a breaker.
type ResilientClient struct {
	baseURL string
	client  *http.Client
	retry   RetryPolicy
	breaker *Breaker
	logger  *slog.Logger

	// PerAttemptTimeout bounds a single try. The caller's context bounds the
	// whole operation - both are needed.
	PerAttemptTimeout time.Duration
}

func NewResilientClient(baseURL string, logger *slog.Logger) *ResilientClient {
	if logger == nil {
		logger = slog.Default()
	}

	return &ResilientClient{
		baseURL: baseURL,
		client: &http.Client{
			// A Transport with explicit timeouts. The zero-value
			// http.DefaultTransport has no response header timeout, so a
			// server that accepts a connection and then goes quiet holds the
			// caller until the context expires.
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   2 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   2 * time.Second,
				ResponseHeaderTimeout: 5 * time.Second,
				ExpectContinueTimeout: time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConnsPerHost:   10,
			},
			// No Client.Timeout: the per-attempt context below is finer
			// grained, and Client.Timeout would cut the retries off too.
		},
		retry: RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   50 * time.Millisecond,
			MaxDelay:    time.Second,
			Jitter:      true,
			OnRetry: func(attempt int, delay time.Duration, err error) {
				logger.Warn("retrying",
					slog.Int("attempt", attempt),
					slog.Duration("delay", delay),
					slog.String("error", err.Error()))
			},
		},
		breaker: NewBreaker(BreakerConfig{
			Name:            baseURL,
			MinimumRequests: 5,
			FailureRatio:    0.5,
			Window:          5 * time.Second,
			Cooldown:        2 * time.Second,
			HalfOpenProbes:  1,
			OnStateChange: func(name string, from, to State) {
				logger.Warn("circuit breaker state change",
					slog.String("dependency", name),
					slog.String("from", from.String()),
					slog.String("to", to.String()))
			},
		}),
		logger:            logger,
		PerAttemptTimeout: 1500 * time.Millisecond,
	}
}

func (c *ResilientClient) Breaker() *Breaker { return c.breaker }

// Get fetches a path with the full policy applied.
func (c *ResilientClient) Get(ctx context.Context, path string) ([]byte, error) {
	var body []byte

	// Breaker outermost.
	err := c.breaker.Do(ctx, func(ctx context.Context) error {
		// Retry in the middle.
		return Do(ctx, c.retry, func(ctx context.Context) error {
			// Per-attempt timeout innermost.
			attemptCtx, cancel := context.WithTimeout(ctx, c.PerAttemptTimeout)
			defer cancel()

			data, err := c.do(attemptCtx, http.MethodGet, path, nil)
			if err != nil {
				return err
			}

			body = data

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return body, nil
}

// Post is deliberately different: a write is only retried when the caller
// supplies an idempotency key, because retrying a non-idempotent write can
// charge a customer twice.
func (c *ResilientClient) Post(ctx context.Context, path, idempotencyKey string, payload io.Reader) ([]byte, error) {
	policy := c.retry

	if idempotencyKey == "" {
		// One attempt. The breaker still applies: failing fast is safe for
		// any operation.
		policy.MaxAttempts = 1
	}

	var body []byte

	err := c.breaker.Do(ctx, func(ctx context.Context) error {
		return Do(ctx, policy, func(ctx context.Context) error {
			attemptCtx, cancel := context.WithTimeout(ctx, c.PerAttemptTimeout)
			defer cancel()

			data, err := c.do(attemptCtx, http.MethodPost, path, idempotencyKey)
			if err != nil {
				return err
			}

			body = data

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return body, nil
}

func (c *ResilientClient) do(ctx context.Context, method, path string, idempotencyKey any) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, PermanentError{Err: err}
	}

	if key, ok := idempotencyKey.(string); ok && key != "" {
		request.Header.Set("Idempotency-Key", key)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}

	defer func() {
		// Draining before closing lets the connection be reused; closing
		// without draining forces a new TCP handshake for the next call.
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
			c.logger.Debug("drain body", slog.String("error", err.Error()))
		}

		if err := response.Body.Close(); err != nil {
			c.logger.Debug("close body", slog.String("error", err.Error()))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if response.StatusCode >= 400 {
		statusError := &HTTPStatusError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
			Body:       string(body),
		}

		// Retry-After is the server telling the client how long to wait. It
		// always wins over the client's own backoff.
		if value := response.Header.Get("Retry-After"); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil {
				statusError.RetryAfter = time.Duration(seconds) * time.Second
			}
		}

		return nil, statusError
	}

	return body, nil
}

// Describe renders the policy, for the demo output.
func (c *ResilientClient) Describe() string {
	return fmt.Sprintf(
		"per-attempt timeout %s, %d attempts, backoff %s..%s with jitter, breaker opens at %.0f%% of %d requests in %s, cooldown %s",
		c.PerAttemptTimeout,
		c.retry.MaxAttempts,
		c.retry.BaseDelay,
		c.retry.MaxDelay,
		c.breaker.config.FailureRatio*100,
		c.breaker.config.MinimumRequests,
		c.breaker.config.Window,
		c.breaker.config.Cooldown,
	)
}
