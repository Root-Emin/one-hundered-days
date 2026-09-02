package perf_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/perf"
)

func TestRunCollectsLatencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Millisecond)

		if _, err := w.Write([]byte("ok")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))

	t.Cleanup(server.Close)

	result, err := perf.Run(t.Context(), server.Client(), perf.Options{
		Label: "test", URL: server.URL, Workers: 4, Requests: 40,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Requests != 40 {
		t.Errorf("requests = %d, want 40", result.Requests)
	}

	if result.Errors != 0 {
		t.Errorf("errors = %d, want 0", result.Errors)
	}

	// Percentiles must be ordered; an out-of-order p95 means the sort or the
	// index arithmetic is wrong, and every number on the report is then junk.
	if !(result.P50 <= result.P95 && result.P95 <= result.P99 && result.P99 <= result.Max) {
		t.Errorf("percentiles out of order: p50=%s p95=%s p99=%s max=%s",
			result.P50, result.P95, result.P99, result.Max)
	}

	if result.Throughput <= 0 {
		t.Errorf("throughput = %f", result.Throughput)
	}
}

func TestRunCountsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))

	t.Cleanup(server.Close)

	_, err := perf.Run(t.Context(), server.Client(), perf.Options{
		Label: "failing", URL: server.URL, Workers: 2, Requests: 6,
	})

	// Every request failed, so there is nothing to report a percentile over -
	// returning a Result full of zeros would look like a fast service.
	if err == nil {
		t.Fatal("expected an error when every request fails")
	}
}

func TestBudgetPasses(t *testing.T) {
	budget := perf.Budget{
		Name:                 "GET /dashboard",
		MaxP95:               10 * time.Millisecond,
		MaxQueriesPerRequest: 2,
		MaxAllocsPerOp:       2000,
		MinThroughput:        100,
	}

	result := perf.Result{P95: 3 * time.Millisecond, Throughput: 500}

	if err := budget.Check(result, 1, 1500); err != nil {
		t.Errorf("budget should pass: %v", err)
	}
}

// The gate has to name every violation, not just the first: a regression
// usually breaks several numbers at once, and fixing them one build at a time
// is how a green pipeline takes a week.
func TestBudgetReportsEveryViolation(t *testing.T) {
	budget := perf.Budget{
		Name:                 "GET /dashboard",
		MaxP95:               5 * time.Millisecond,
		MaxQueriesPerRequest: 2,
		MaxAllocsPerOp:       1000,
		MinThroughput:        1000,
	}

	result := perf.Result{P95: 50 * time.Millisecond, Throughput: 200}

	err := budget.Check(result, 51, 5000)
	if err == nil {
		t.Fatal("expected the budget to fail")
	}

	if !errors.Is(err, perf.ErrBudgetExceeded) {
		t.Errorf("error = %v, want ErrBudgetExceeded", err)
	}

	for _, want := range []string{"p95", "queries per request", "allocs/op", "req/s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// A zero threshold means "not checked", so a partially configured budget does
// not silently fail on the fields nobody set.
func TestZeroThresholdsAreIgnored(t *testing.T) {
	budget := perf.Budget{Name: "partial", MaxQueriesPerRequest: 2}

	result := perf.Result{P95: time.Hour, Throughput: 0}

	if err := budget.Check(result, 1, 999999); err != nil {
		t.Errorf("unset thresholds should not be checked: %v", err)
	}
}
