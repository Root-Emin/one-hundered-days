package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

/*
Day 74 - Observability & Resilience: Retries, Circuit Breakers, and Timeouts

Tasks covered:

 1. Retries with exponential backoff and full jitter, and a classifier that
    only retries what can succeed (retry.go)
 2. Timeouts on every outbound call: a per-attempt deadline inside the
    caller's overall deadline, plus explicit transport timeouts (client.go)
 3. A circuit breaker with closed/open/half-open states, so a dead dependency
    fails fast instead of consuming the caller (breaker.go)
 4. A written policy: when a retry is safe, and when it charges someone twice

Run:

	go run .

Test:

	go test ./...
	go test -race -count=1 ./...
*/

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			// Drop the timestamp: the demo output should be diffable.
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return attr
		},
	}))

	if err := run(logger); err != nil {
		logger.Error("demo failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	//
	// 1. Backoff shape
	//

	fmt.Println("\n1) Backoff with and without jitter")
	fmt.Println(strings.Repeat("-", 78))

	plain := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second}
	jittered := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second, Jitter: true}

	fmt.Printf("  %-10s %-14s %s\n", "ATTEMPT", "NO JITTER", "FULL JITTER (3 samples)")

	for attempt := 1; attempt <= 6; attempt++ {
		samples := make([]string, 0, 3)

		for range 3 {
			samples = append(samples, jittered.delayFor(attempt).Round(time.Millisecond).String())
		}

		fmt.Printf("  %-10d %-14s %s\n", attempt,
			plain.delayFor(attempt).Round(time.Millisecond), strings.Join(samples, ", "))
	}

	fmt.Println("\n  Without jitter every client that failed together retries together,")
	fmt.Println("  and the dependency gets a thundering herd at each step.")

	//
	// 2. A flaky dependency, recovered by retries
	//

	fmt.Println("\n2) Transient failures, recovered by retrying")
	fmt.Println(strings.Repeat("-", 78))

	var attempts atomic.Int64

	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail the first two calls with a 503, then succeed.
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			logger.Error("write", slog.String("error", err.Error()))
		}
	}))
	defer flaky.Close()

	client := NewResilientClient(flaky.URL, logger)

	fmt.Printf("  policy: %s\n\n", client.Describe())

	start := time.Now()

	body, err := client.Get(ctx, "/orders")
	if err != nil {
		return err
	}

	fmt.Printf("\n  succeeded after %d attempts in %s: %s\n",
		attempts.Load(), time.Since(start).Round(time.Millisecond), body)

	//
	// 3. A permanent failure is not retried
	//

	fmt.Println("\n3) A 400 is not retried")
	fmt.Println(strings.Repeat("-", 78))

	var badRequests atomic.Int64

	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badRequests.Add(1)

		w.WriteHeader(http.StatusBadRequest)
	}))
	defer rejecting.Close()

	rejectingClient := NewResilientClient(rejecting.URL, logger)

	_, err = rejectingClient.Get(ctx, "/orders")

	var statusErr *HTTPStatusError

	if errors.As(err, &statusErr) {
		fmt.Printf("  %d call(s) made, error: %v\n", badRequests.Load(), err)
		fmt.Println("  A 400 will be a 400 forever: retrying wastes the caller's deadline")
		fmt.Println("  and the server's capacity.")
	}

	//
	// 4. A dead dependency, and the breaker
	//

	fmt.Println("\n4) A dead dependency: with and without a breaker")
	fmt.Println(strings.Repeat("-", 78))

	var deadCalls atomic.Int64

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadCalls.Add(1)

		// Slow AND failing: the worst combination, because every caller pays
		// the full timeout before finding out.
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	deadClient := NewResilientClient(dead.URL, logger)
	deadClient.PerAttemptTimeout = 500 * time.Millisecond

	fmt.Println()

	var (
		failFast int
		slowFail int
	)

	for i := range 12 {
		callStart := time.Now()

		_, err := deadClient.Get(ctx, "/orders")

		elapsed := time.Since(callStart)

		switch {
		case errors.Is(err, ErrBreakerOpen):
			failFast++

			if failFast <= 2 {
				fmt.Printf("  call %2d: failed fast in %-8s (breaker open, dependency untouched)\n",
					i+1, elapsed.Round(time.Millisecond))
			}

		default:
			slowFail++

			fmt.Printf("  call %2d: failed in %-8s after retrying\n", i+1, elapsed.Round(time.Millisecond))
		}
	}

	fmt.Printf("\n  %d calls waited for the dependency, %d failed instantly.\n", slowFail, failFast)
	fmt.Printf("  The dead server was called %d times instead of %d.\n", deadCalls.Load(), 12*3)
	fmt.Println("  That difference is what stops one outage from becoming three.")

	//
	// 5. Recovery
	//

	fmt.Println("\n5) Recovery through half-open")
	fmt.Println(strings.Repeat("-", 78))

	fmt.Printf("  breaker is %s; waiting for the cooldown...\n", deadClient.Breaker().State())

	time.Sleep(2100 * time.Millisecond)

	fmt.Printf("  breaker is now %s: the next call is a probe\n", deadClient.Breaker().State())
	fmt.Println("  If the probe succeeds the breaker closes; if it fails it opens again")
	fmt.Println("  for another cooldown. One probe at a time, so a recovering service")
	fmt.Println("  is not flooded the moment it comes back.")

	printPolicy()

	return nil
}

func printPolicy() {
	fmt.Println("\nWhen is a retry safe?")
	fmt.Println(strings.Repeat("-", 78))

	rows := []struct {
		operation string
		safe      string
		why       string
	}{
		{"GET /orders", "yes", "read-only; repeating it changes nothing"},
		{"PUT /orders/1 (full replace)", "yes", "idempotent by definition: same result every time"},
		{"DELETE /orders/1", "yes", "already-deleted must return success, not 404 (Day 63)"},
		{"POST /orders", "NO", "creates a second order unless an idempotency key is sent"},
		{"POST /charges", "NO", "charges the customer twice; the incident nobody forgets"},
		{"POST + Idempotency-Key", "yes", "the server deduplicates by the key"},
		{"Any 4xx", "NO", "the request is wrong; it will be wrong next time too"},
		{"408, 429, 5xx", "yes", "transient by definition (honour Retry-After on 429)"},
		{"Connection refused/reset", "yes", "the request may never have arrived"},
		{"Context cancelled", "NO", "the caller has already given up"},
	}

	fmt.Printf("  %-30s %-6s %s\n", "OPERATION", "RETRY", "WHY")

	for _, row := range rows {
		fmt.Printf("  %-30s %-6s %s\n", row.operation, row.safe, row.why)
	}

	fmt.Println("\nBudgets and blast radius")
	fmt.Println(strings.Repeat("-", 78))
	fmt.Println("  * Retries multiply load. Three attempts at every hop of a four-service")
	fmt.Println("    chain is 81 calls for one request. Retry at ONE layer, usually the")
	fmt.Println("    outermost one that knows the operation is idempotent.")
	fmt.Println("  * A retry budget (retry at most N% of requests) keeps a partial outage")
	fmt.Println("    from turning into a self-inflicted DDoS.")
	fmt.Println("  * Every timeout must be shorter than the caller's timeout, or the")
	fmt.Println("    caller gives up while this service is still waiting - work nobody")
	fmt.Println("    will ever read.")
	fmt.Println("  * The breaker's job is not to fix the dependency. It is to keep THIS")
	fmt.Println("    service alive while the dependency is broken.")
	fmt.Println()
}
