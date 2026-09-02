// Package metrics is Linkr's Prometheus instrumentation.
//
// Four series, chosen with the RED method in mind - Rate, Errors, Duration -
// plus the two numbers specific to this service: the cache hit ratio and the
// outbox depth.
//
// The rule that governs every label here is CARDINALITY. A Prometheus series
// exists for each unique combination of label values, and each one costs
// memory in the server forever. Putting a link code in a label would create a
// series per short link: a million links is a million series, and the
// monitoring system falls over before the service does.
//
// So: the route TEMPLATE, never the path. The status CLASS as well as the code.
// Never a user id, a code, a URL or an error message.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the collectors.
type Metrics struct {
	registry *prometheus.Registry

	requests   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	inFlight   prometheus.Gauge
	redirects  *prometheus.CounterVec
	cacheHits  *prometheus.CounterVec
	outboxSize prometheus.Gauge
	clicksLost prometheus.Counter
}

// New builds the collectors on their own registry.
//
// Own registry rather than the default: the default is global mutable state
// that any dependency can register into, and a duplicate registration panics
// at startup. An explicit registry also means a test can build a fresh one.
func New() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,

		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "linkr_http_requests_total",
			Help: "HTTP requests by route template, method and status class.",
		}, []string{"route", "method", "status", "class"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "linkr_http_request_duration_seconds",
			Help: "Request duration by route template.",
			// Buckets chosen around the SLO the requirements promise: p95
			// under 5ms for a cached redirect. Default buckets start at 5ms,
			// which would put every good redirect in the first bucket and
			// make the p95 unmeasurable.
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.5, 1, 5},
		}, []string{"route", "method"}),

		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "linkr_http_requests_in_flight",
			Help: "Requests currently being served.",
		}),

		redirects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "linkr_redirects_total",
			Help: "Redirect outcomes.",
		}, []string{"outcome"}),

		cacheHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "linkr_redirect_cache_total",
			Help: "Redirect cache lookups.",
		}, []string{"result"}),

		outboxSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "linkr_outbox_pending",
			Help: "Click events waiting to be aggregated.",
		}),

		clicksLost: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "linkr_clicks_dropped_total",
			Help: "Clicks that could not be recorded after the redirect was served.",
		}),
	}

	registry.MustRegister(m.requests, m.duration, m.inFlight, m.redirects,
		m.cacheHits, m.outboxSize, m.clicksLost)

	// Initialise the series that matter at zero. A dashboard querying a metric
	// that has never been incremented shows "no data", which looks the same as
	// "the service is down" - and during an incident that costs minutes.
	for _, outcome := range []string{"found", "not_found", "gone", "error"} {
		m.redirects.WithLabelValues(outcome)
	}

	for _, result := range []string{"hit", "miss", "negative_hit"} {
		m.cacheHits.WithLabelValues(result)
	}

	return m
}

// Handler serves /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Registry exposes the registry, for tests and for a gatherer.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// RouteTemplate maps a request path to a BOUNDED label value.
//
// This function is the cardinality guard. "/abc123" and "/xyz789" both become
// "/{code}", so a million links produce one series rather than a million.
func RouteTemplate(path string) string {
	switch {
	case path == "/" || path == "":
		return "/"
	case path == "/healthz", path == "/readyz", path == "/metrics":
		return path
	case path == "/api/links":
		return "/api/links"
	case len(path) > len("/api/links/") && path[:len("/api/links/")] == "/api/links/":
		return "/api/links/{code}"
	default:
		return "/{code}"
	}
}

// statusClass turns a code into 2xx/3xx/4xx/5xx.
//
// The class is what an alert uses: "the 5xx rate is above 1%" is a rule you
// can write once, where "the 503 rate plus the 500 rate plus…" is a rule that
// misses the next status code someone returns.
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// Middleware records the RED metrics for every request.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := RouteTemplate(r.URL.Path)

		m.inFlight.Inc()
		defer m.inFlight.Dec()

		start := time.Now()

		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		m.duration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())

		m.requests.WithLabelValues(
			route, r.Method, strconv.Itoa(recorder.status), statusClass(recorder.status)).Inc()
	})
}

type statusWriter struct {
	http.ResponseWriter

	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status

	w.ResponseWriter.WriteHeader(status)
}

// RecordRedirect counts a redirect outcome: found, not_found, gone or error.
func (m *Metrics) RecordRedirect(outcome string) {
	m.redirects.WithLabelValues(outcome).Inc()
}

// RecordCacheLookup counts a cache result: hit, negative_hit or miss.
func (m *Metrics) RecordCacheLookup(result string) {
	m.cacheHits.WithLabelValues(result).Inc()
}

// SetOutboxPending publishes the queue depth.
//
// This is the alert that catches a dead worker: the redirect keeps working and
// the numbers on the dashboard keep rising, and nothing else would notice for
// hours.
func (m *Metrics) SetOutboxPending(count int) {
	m.outboxSize.Set(float64(count))
}

// RecordClickDropped counts a click that could not be stored.
func (m *Metrics) RecordClickDropped() {
	m.clicksLost.Inc()
}
