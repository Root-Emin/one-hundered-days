package metrics_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-72/internal/metrics"
)

func newTestHandler(t *testing.T) (*metrics.Metrics, http.Handler) {
	t.Helper()

	recorder := metrics.New("test")

	mux := http.NewServeMux()

	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "999" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if _, err := w.Write([]byte(`{"id":1}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return recorder, recorder.Middleware(mux)
}

func call(t *testing.T, handler http.Handler, method, path string) int {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	return recorder.Code
}

func TestRequestsAreCounted(t *testing.T) {
	t.Parallel()

	recorder, handler := newTestHandler(t)

	call(t, handler, http.MethodGet, "/orders/1")
	call(t, handler, http.MethodGet, "/orders/2")
	call(t, handler, http.MethodGet, "/orders/999")

	// Two successes and one 404, all under ONE route label.
	if got := testutil.ToFloat64(recorder.RequestsTotal.WithLabelValues("GET", "/orders/{id}", "2xx")); got != 2 {
		t.Fatalf("2xx count = %v, want 2", got)
	}

	if got := testutil.ToFloat64(recorder.RequestsTotal.WithLabelValues("GET", "/orders/{id}", "4xx")); got != 1 {
		t.Fatalf("4xx count = %v, want 1", got)
	}
}

// TestCardinalityIsBounded is the test that protects the metrics backend: 200
// different URLs must not produce 200 time series.
func TestCardinalityIsBounded(t *testing.T) {
	t.Parallel()

	recorder, handler := newTestHandler(t)

	for i := range 200 {
		call(t, handler, http.MethodGet, "/orders/"+strings.Repeat("1", 1+i%3)+string(rune('0'+i%10)))
	}

	// Every one of those matched the same template.
	series := testutil.CollectAndCount(recorder.RequestsTotal)

	if series > 3 {
		t.Fatalf("%d time series from 200 distinct URLs - the labels are unbounded", series)
	}
}

// TestUnmatchedPathsAreSanitised covers the other half: a request that matches
// no route at all still must not create a series per URL.
func TestUnmatchedPathsAreSanitised(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/orders/12345":          "/orders/{id}",
		"/orders/12345/items/67": "/orders/{id}/items/{id}",
		"/a/b/c/d/e/f":           "/other",
		"/verylongsegment" + strings.Repeat("x", 40): "/{id}",
		"/": "/",
	}

	for path, want := range tests {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)

		if got := metrics.RouteTemplate(request); got != want {
			t.Errorf("RouteTemplate(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLatencyIsObserved(t *testing.T) {
	t.Parallel()

	recorder, handler := newTestHandler(t)

	call(t, handler, http.MethodGet, "/healthz")

	count := testutil.CollectAndCount(recorder.RequestDuration)

	if count == 0 {
		t.Fatal("no latency observations were recorded")
	}
}

func TestInFlightReturnsToZero(t *testing.T) {
	t.Parallel()

	recorder, handler := newTestHandler(t)

	call(t, handler, http.MethodGet, "/healthz")

	if got := testutil.ToFloat64(recorder.RequestsInFlight); got != 0 {
		t.Fatalf("in flight = %v after the request finished, want 0 - the gauge leaks", got)
	}
}

func TestDatabaseObservationClassifiesErrors(t *testing.T) {
	t.Parallel()

	recorder := metrics.New("test")

	failures := map[string]error{
		"timeout":    errors.New("context deadline exceeded"),
		"connection": errors.New("connection reset by peer"),
		"constraint": errors.New("duplicate key value violates unique constraint"),
		"other":      errors.New("something else entirely"),
	}

	for kind, err := range failures {
		if observeErr := recorder.ObserveDatabase("insert", func() error { return err }); observeErr == nil {
			t.Fatal("ObserveDatabase swallowed the error")
		}

		if got := testutil.ToFloat64(recorder.DatabaseErrors.WithLabelValues("insert", kind)); got != 1 {
			t.Errorf("%s count = %v, want 1", kind, got)
		}
	}

	// A successful call is timed but not counted as an error.
	if err := recorder.ObserveDatabase("select", func() error { return nil }); err != nil {
		t.Fatalf("ObserveDatabase: %v", err)
	}

	if got := testutil.CollectAndCount(recorder.DatabaseDuration); got == 0 {
		t.Fatal("successful calls are not timed")
	}
}

func TestMetricsEndpointServesTheRegistry(t *testing.T) {
	t.Parallel()

	recorder, handler := newTestHandler(t)

	call(t, handler, http.MethodGet, "/healthz")

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	recorder.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	body := response.Body.String()

	for _, expected := range []string{
		"test_http_requests_total",
		"test_http_request_duration_seconds_bucket",
		"test_http_requests_in_flight",
		"go_goroutines", // the runtime collector is registered too
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("/metrics does not expose %s", expected)
		}
	}

	// Prometheus naming conventions, checked rather than assumed.
	if strings.Contains(body, "test_http_requests_total_total") {
		t.Error("a counter got a doubled _total suffix")
	}
}

// TestRegistryIsIsolated: each Metrics has its own registry, so tests do not
// fight over a global.
func TestRegistryIsIsolated(t *testing.T) {
	t.Parallel()

	first := metrics.New("test")
	second := metrics.New("test")

	first.OrdersTotal.WithLabelValues("created").Inc()

	if got := testutil.ToFloat64(second.OrdersTotal.WithLabelValues("created")); got != 0 {
		t.Fatalf("second registry sees %v, want 0", got)
	}
}
