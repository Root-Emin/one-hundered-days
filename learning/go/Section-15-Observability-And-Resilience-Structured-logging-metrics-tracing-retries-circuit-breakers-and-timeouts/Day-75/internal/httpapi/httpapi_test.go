package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/httpapi"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/observability"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/orders"
)

func newTestServer(t *testing.T) (*httptest.Server, *observability.Observability, *orders.Service) {
	t.Helper()

	obs, err := observability.Setup(t.Context(), observability.Config{
		ServiceName: "test",
		Environment: "test",
		LogFormat:   "text",
		SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("observability: %v", err)
	}

	t.Cleanup(func() {
		if err := obs.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	service := orders.NewService(obs)

	server := httptest.NewServer(httpapi.New(obs, service).Routes())

	t.Cleanup(server.Close)

	return server, obs, service
}

func post(t *testing.T, server *httptest.Server, path string, body any) (*http.Response, []byte) {
	t.Helper()

	var reader *bytes.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	return response, buffer.Bytes()
}

func TestHappyPathIsInstrumented(t *testing.T) {
	server, obs, _ := newTestServer(t)

	response, body := post(t, server, "/orders", map[string]any{"customer": "ada", "cents": 1299})

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d (%s)", response.StatusCode, body)
	}

	// Every response carries the trace id, so a user can quote it in a
	// support ticket and an engineer can find the trace.
	traceID := response.Header.Get("X-Trace-Id")

	if len(traceID) != 32 {
		t.Fatalf("X-Trace-Id = %q, want a 32 character trace id", traceID)
	}

	var payload struct {
		TraceID string `json:"trace_id"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if payload.TraceID != traceID {
		t.Fatalf("body trace id %q != header %q", payload.TraceID, traceID)
	}

	// And the metrics moved.
	if got := testutil.ToFloat64(
		obs.Metrics.RequestsTotal.WithLabelValues("POST", "/orders", "2xx")); got != 1 {
		t.Fatalf("request counter = %v, want 1", got)
	}

	if got := testutil.ToFloat64(obs.Metrics.OrdersTotal.WithLabelValues("created")); got != 1 {
		t.Fatalf("orders counter = %v, want 1", got)
	}
}

func TestValidationIsRejectedAndCounted(t *testing.T) {
	server, obs, _ := newTestServer(t)

	response, _ := post(t, server, "/orders", map[string]any{"customer": "", "cents": 0})

	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}

	if got := testutil.ToFloat64(obs.Metrics.OrdersTotal.WithLabelValues("rejected")); got != 1 {
		t.Fatalf("rejected counter = %v, want 1", got)
	}

	// A 4xx must not be counted as a server error, or the error-rate alert
	// fires on client mistakes.
	if got := testutil.ToFloat64(
		obs.Metrics.RequestsTotal.WithLabelValues("POST", "/orders", "5xx")); got != 0 {
		t.Fatalf("5xx counter = %v after a client error", got)
	}
}

// TestBreakerOpensAndIsVisible is the capstone assertion: a failing dependency
// trips the breaker, the API says 503 with Retry-After, /readyz reports it,
// and the metric reflects it.
func TestBreakerOpensAndIsVisible(t *testing.T) {
	server, obs, service := newTestServer(t)

	service.Chaos.PaymentFailureRate.Store(100)

	var sawUnavailable bool

	for range 20 {
		response, _ := post(t, server, "/orders", map[string]any{"customer": "ada", "cents": 100})

		if response.StatusCode == http.StatusServiceUnavailable {
			sawUnavailable = true

			if response.Header.Get("Retry-After") == "" {
				t.Fatal("503 without a Retry-After header")
			}

			break
		}
	}

	if !sawUnavailable {
		t.Fatal("the breaker never opened under a fully failing dependency")
	}

	if service.BreakerState() != "open" {
		t.Fatalf("breaker = %s, want open", service.BreakerState())
	}

	// 2 == open, per the metric's documented encoding.
	if got := testutil.ToFloat64(obs.Metrics.BreakerState.WithLabelValues("payments")); got != 2 {
		t.Fatalf("breaker metric = %v, want 2 (open)", got)
	}

	// Readiness turns red so a load balancer stops sending traffic, while
	// liveness stays green so the orchestrator does not restart the pod.
	readiness := get(t, server, "/readyz")

	if readiness != http.StatusServiceUnavailable {
		t.Fatalf("readyz = %d, want 503 while the breaker is open", readiness)
	}

	if liveness := get(t, server, "/healthz"); liveness != http.StatusOK {
		t.Fatalf("healthz = %d, want 200: liveness must not depend on a dependency", liveness)
	}
}

func TestBreakerRecovers(t *testing.T) {
	server, _, service := newTestServer(t)

	service.Chaos.PaymentFailureRate.Store(100)

	for range 20 {
		post(t, server, "/orders", map[string]any{"customer": "ada", "cents": 100})

		if service.BreakerState() == "open" {
			break
		}
	}

	if service.BreakerState() != "open" {
		t.Fatal("the breaker did not open")
	}

	// The dependency recovers, and the cooldown passes.
	service.Chaos.PaymentFailureRate.Store(0)

	time.Sleep(3200 * time.Millisecond)

	response, body := post(t, server, "/orders", map[string]any{"customer": "ada", "cents": 100})

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("probe request = %d (%s)", response.StatusCode, body)
	}

	if state := service.BreakerState(); state != "closed" {
		t.Fatalf("breaker = %s after a successful probe, want closed", state)
	}
}

func TestMetricsEndpointIsNotCountedAsTraffic(t *testing.T) {
	server, obs, _ := newTestServer(t)

	for range 3 {
		if status := get(t, server, "/metrics"); status != http.StatusOK {
			t.Fatalf("metrics = %d", status)
		}
	}

	// A scrape is not user traffic. Counting it inflates the request rate and
	// hides a real drop in traffic.
	if got := testutil.ToFloat64(
		obs.Metrics.RequestsTotal.WithLabelValues("GET", "/metrics", "2xx")); got != 0 {
		t.Fatalf("scrapes were counted as requests: %v", got)
	}
}

func TestMetricsExposeTheExpectedSeries(t *testing.T) {
	server, _, _ := newTestServer(t)

	post(t, server, "/orders", map[string]any{"customer": "ada", "cents": 100})

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/metrics", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(response.Body); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close: %v", err)
	}

	body := buffer.String()

	// The panels in docs/DASHBOARD.md depend on these existing. A test is how
	// a dashboard stops silently breaking when someone renames a metric.
	for _, series := range []string{
		"test_http_requests_total",
		"test_http_request_duration_seconds_bucket",
		"test_http_requests_in_flight",
		"test_dependency_calls_total",
		"test_dependency_breaker_state",
		"test_dependency_retries_total",
		"test_orders_processed_total",
		"go_goroutines",
	} {
		if !strings.Contains(body, series) {
			t.Errorf("/metrics is missing %s, which a dashboard panel queries", series)
		}
	}
}

func get(t *testing.T, server *httptest.Server, path string) int {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	return response.StatusCode
}
