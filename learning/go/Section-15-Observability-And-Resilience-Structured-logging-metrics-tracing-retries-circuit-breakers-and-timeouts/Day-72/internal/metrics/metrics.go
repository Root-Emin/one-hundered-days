// Package metrics defines the service's Prometheus metrics and the middleware
// that records them.
//
// Three rules run through this file:
//
//  1. Metrics are summaries, not events. They answer "how many, how fast, how
//     full" - never "what happened to this one request" (that is a log or a
//     trace).
//  2. Every label value must come from a bounded set. A label whose values are
//     unbounded (a user id, a raw URL path, an error message) multiplies the
//     number of time series until the metrics backend falls over.
//  3. Name them the way Prometheus expects: <namespace>_<subsystem>_<unit>,
//     counters end in _total, durations are seconds.
package metrics

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry *prometheus.Registry

	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge
	ResponseSize     *prometheus.HistogramVec

	// Business metrics: the ones a product owner would ask about.
	OrdersTotal *prometheus.CounterVec
	OrderValue  prometheus.Histogram
	QueueDepth  prometheus.Gauge

	// Dependency metrics: the ones that explain a latency spike.
	DatabaseDuration *prometheus.HistogramVec
	DatabaseErrors   *prometheus.CounterVec
}

// New builds a registry with the service's metrics plus the Go runtime and
// process collectors.
//
// A private registry rather than the global default: it makes the metric set
// explicit, and it lets a test build a fresh one instead of fighting state
// left behind by another test.
func New(namespace string) *Metrics {
	registry := prometheus.NewRegistry()

	metrics := &Metrics{
		Registry: registry,

		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "requests_total",
				Help:      "Total HTTP requests by method, route template and status class.",
			},
			// route is a TEMPLATE ("/orders/{id}"), never the raw path.
			// status is a CLASS ("2xx"), not the exact code, because the code
			// rarely changes a decision and doubles the series count.
			[]string{"method", "route", "status"},
		),

		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "request_duration_seconds",
				Help:      "HTTP request latency in seconds.",
				// Buckets decide which percentiles are answerable. The
				// defaults stop at 10s; these are tuned for a web API where
				// anything over 2s is already an incident.
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
			},
			[]string{"method", "route"},
		),

		RequestsInFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "requests_in_flight",
				Help:      "Requests currently being served. This is the saturation signal.",
			},
		),

		ResponseSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "response_size_bytes",
				Help:      "Response size in bytes.",
				Buckets:   prometheus.ExponentialBuckets(64, 4, 8),
			},
			[]string{"route"},
		),

		OrdersTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "orders",
				Name:      "created_total",
				Help:      "Orders created, by outcome.",
			},
			// "outcome" has three possible values, forever. That is what a
			// good label looks like.
			[]string{"outcome"},
		),

		OrderValue: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "orders",
				Name:      "value_cents",
				Help:      "Distribution of order values in cents.",
				Buckets:   prometheus.ExponentialBuckets(100, 3, 8),
			},
		),

		QueueDepth: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "orders",
				Name:      "queue_depth",
				Help:      "Orders waiting to be processed.",
			},
		),

		DatabaseDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "database",
				Name:      "query_duration_seconds",
				Help:      "Database query latency by operation.",
				Buckets:   []float64{0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
			},
			// The operation NAME, never the SQL text and never the parameters.
			[]string{"operation"},
		),

		DatabaseErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "database",
				Name:      "errors_total",
				Help:      "Database errors by operation and kind.",
			},
			[]string{"operation", "kind"},
		),
	}

	registry.MustRegister(
		metrics.RequestsTotal,
		metrics.RequestDuration,
		metrics.RequestsInFlight,
		metrics.ResponseSize,
		metrics.OrdersTotal,
		metrics.OrderValue,
		metrics.QueueDepth,
		metrics.DatabaseDuration,
		metrics.DatabaseErrors,

		// The runtime collectors are free and answer half the questions an
		// on-call engineer has: goroutine count, GC pauses, memory, open FDs.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return metrics
}

// Handler serves the scrape endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		// An error while gathering must not take the endpoint down: report it
		// and serve what is available, or a broken collector blinds the whole
		// dashboard.
		ErrorHandling: promhttp.ContinueOnError,
		// Compression matters: a service with many series produces a large
		// body on every scrape.
		EnableOpenMetrics: true,
	})
}

//
// MIDDLEWARE
//

// Middleware records the RED metrics: Rate, Errors, Duration.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		m.RequestsInFlight.Inc()
		defer m.RequestsInFlight.Dec()

		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		route := RouteTemplate(r)
		duration := time.Since(start).Seconds()

		m.RequestsTotal.WithLabelValues(r.Method, route, statusClass(recorder.status)).Inc()
		m.RequestDuration.WithLabelValues(r.Method, route).Observe(duration)
		m.ResponseSize.WithLabelValues(route).Observe(float64(recorder.written))
	})
}

type responseRecorder struct {
	http.ResponseWriter

	status  int
	written int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	written, err := r.ResponseWriter.Write(data)

	r.written += written

	return written, err
}

//
// CARDINALITY CONTROL
//

// numericSegment matches path segments that are obviously identifiers.
var numericSegment = regexp.MustCompile(`^\d+$`)

// RouteTemplate turns a request into a bounded label value.
//
// Go 1.22's ServeMux exposes the matched pattern, which is exactly the
// template we want. When there is no pattern (a 404), the raw path is
// replaced with a placeholder: an attacker requesting /aaaa, /aaab, /aaac
// would otherwise create one time series per request.
func RouteTemplate(r *http.Request) string {
	if pattern := r.Pattern; pattern != "" {
		// "GET /orders/{id}" -> "/orders/{id}"
		if _, path, found := strings.Cut(pattern, " "); found {
			return path
		}

		return pattern
	}

	return sanitisePath(r.URL.Path)
}

// sanitisePath is the fallback for unmatched routes: it collapses numeric
// segments and refuses to invent more than a handful of series.
func sanitisePath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")

	if len(segments) > 4 {
		return "/other"
	}

	for i, segment := range segments {
		if numericSegment.MatchString(segment) || len(segment) > 32 {
			segments[i] = "{id}"
		}
	}

	joined := "/" + strings.Join(segments, "/")

	if joined == "/" {
		return "/"
	}

	return joined
}

// statusClass collapses 404 and 410 into "4xx". The exact code belongs in the
// log line; the metric only needs the class to drive an alert.
func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}

	return strconv.Itoa(status/100) + "xx"
}

//
// HELPERS FOR NON-HTTP WORK
//

// ObserveDatabase times a database call and records its outcome.
//
// Wrapping the call rather than sprinkling timers keeps the two metrics
// consistent: every timed call is also counted when it fails.
func (m *Metrics) ObserveDatabase(operation string, fn func() error) error {
	start := time.Now()

	err := fn()

	m.DatabaseDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())

	if err != nil {
		// The KIND of error, from a fixed set - never err.Error(), which is
		// unbounded and would explode the cardinality.
		m.DatabaseErrors.WithLabelValues(operation, classifyError(err)).Inc()
	}

	return err
}

func classifyError(err error) string {
	message := strings.ToLower(err.Error())

	switch {
	case strings.Contains(message, "timeout") || strings.Contains(message, "deadline"):
		return "timeout"
	case strings.Contains(message, "connection"):
		return "connection"
	case strings.Contains(message, "constraint") || strings.Contains(message, "duplicate"):
		return "constraint"
	default:
		return "other"
	}
}
