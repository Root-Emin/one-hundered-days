// Command loadgen drives the running server through a healthy phase, an
// injected failure, and a recovery - so the logs, metrics and breaker can be
// watched changing.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

/*
Run the server first:

	go run ./cmd/server

Then, in another terminal:

	go run ./cmd/loadgen

Watch the server's log stream while this runs, and afterwards:

	curl -s localhost:8080/metrics | grep -E 'day75_(http|dependency|orders)'
*/

func main() {
	baseURL := envOr("BASE_URL", "http://localhost:8080")

	if err := run(baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "loadgen: %v\n", err)
		os.Exit(1)
	}
}

func run(baseURL string) error {
	phases := []struct {
		name     string
		chaos    string
		requests int
		note     string
	}{
		{
			"healthy baseline",
			"database_failure_rate=0&payment_failure_rate=0&slow_rate=0",
			40,
			"this is what the dashboard looks like when nothing is wrong",
		},
		{
			"slow dependency",
			"database_failure_rate=0&payment_failure_rate=0&slow_rate=40",
			40,
			"latency histogram shifts right; error rate stays flat",
		},
		{
			"payments failing hard",
			// 95%: with three attempts, ~86% of operations still fail, which
			// is above the breaker's 50% threshold. At 70% the retries would
			// absorb enough failures to keep the breaker closed - which is
			// the point of having both.
			"database_failure_rate=0&payment_failure_rate=95&slow_rate=10",
			// Long enough to outweigh the successes still sitting in the
			// breaker's 10 second window. A short burst of failures after a
			// healthy period does NOT trip a rolling-window breaker - which
			// is the behaviour you want, and a surprise the first time you
			// watch for it.
			200,
			"retries climb, then the breaker opens and calls start failing fast",
		},
		{
			"recovery",
			"database_failure_rate=0&payment_failure_rate=0&slow_rate=0",
			// Long enough to outlive the breaker's cooldown, so the probe
			// happens while this phase is still running.
			200,
			"calls fail fast until the cooldown expires; then one probe succeeds and the breaker closes",
		},
	}

	for _, phase := range phases {
		fmt.Printf("\n=== %s ===\n%s\n\n", strings.ToUpper(phase.name), phase.note)

		if err := setChaos(baseURL, phase.chaos); err != nil {
			return err
		}

		summary := drive(baseURL, phase.requests)

		fmt.Printf("  created=%d  rejected=%d  unavailable=%d  failed=%d  p95≈%s\n",
			summary.created, summary.rejected, summary.unavailable, summary.failed,
			summary.p95().Round(time.Millisecond))

		readiness, breaker := probeReadiness(baseURL)

		fmt.Printf("  readyz=%d breaker=%s\n", readiness, breaker)

		time.Sleep(time.Second)
	}

	fmt.Println("\nNow look at:")
	fmt.Println("  curl -s localhost:8080/metrics | grep day75_dependency_breaker_state")
	fmt.Println("  curl -s localhost:8080/metrics | grep day75_orders_processed_total")
	fmt.Println("  curl -s localhost:8080/metrics | grep day75_dependency_retries_total")
	fmt.Println()

	return nil
}

type summary struct {
	mu          sync.Mutex
	created     int
	rejected    int
	unavailable int
	failed      int
	latencies   []time.Duration
}

func (s *summary) p95() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.latencies) == 0 {
		return 0
	}

	sorted := append([]time.Duration(nil), s.latencies...)

	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	return sorted[int(float64(len(sorted)-1)*0.95)]
}

func drive(baseURL string, requests int) *summary {
	result := &summary{}

	var (
		waitGroup sync.WaitGroup
		counter   atomic.Int64
	)

	// Eight concurrent clients: enough to make the in-flight gauge move.
	for range 8 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			for counter.Add(1) <= int64(requests) {
				start := time.Now()

				status := createOrder(baseURL)

				elapsed := time.Since(start)

				result.mu.Lock()

				result.latencies = append(result.latencies, elapsed)

				switch status {
				case http.StatusCreated:
					result.created++
				case http.StatusUnprocessableEntity:
					result.rejected++
				case http.StatusServiceUnavailable:
					result.unavailable++
				default:
					result.failed++
				}

				result.mu.Unlock()

				time.Sleep(20 * time.Millisecond)
			}
		}()
	}

	waitGroup.Wait()

	return result
}

func createOrder(baseURL string) int {
	payload, err := json.Marshal(map[string]any{"customer": "loadgen", "cents": 1999})
	if err != nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/orders", bytes.NewReader(payload))
	if err != nil {
		return 0
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0
	}

	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	return response.StatusCode
}

func setChaos(baseURL, query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/debug/chaos?"+query, nil)
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("is the server running? %w", err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	return nil
}

func probeReadiness(baseURL string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/readyz", nil)
	if err != nil {
		return 0, "unknown"
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "unknown"
	}

	defer func() {
		_ = response.Body.Close()
	}()

	var body struct {
		Breaker string `json:"breaker"`
	}

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return response.StatusCode, "unknown"
	}

	return response.StatusCode, body.Breaker
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
