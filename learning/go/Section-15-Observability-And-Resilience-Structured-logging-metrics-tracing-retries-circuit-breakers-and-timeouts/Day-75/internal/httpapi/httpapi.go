// Package httpapi is the transport layer, with the observability middleware
// stack.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/observability"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/orders"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/resilience"
)

type Handler struct {
	observability *observability.Observability
	service       *orders.Service
}

func New(obs *observability.Observability, service *orders.Service) *Handler {
	return &Handler{observability: obs, service: service}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// The scrape endpoint is outside the middleware: a scrape is not user
	// traffic and must not appear in the request rate.
	mux.Handle("GET /metrics", h.observability.Metrics.Handler())

	api := http.NewServeMux()

	api.HandleFunc("GET /healthz", h.health)
	api.HandleFunc("GET /readyz", h.ready)
	api.HandleFunc("POST /orders", h.createOrder)
	api.HandleFunc("GET /orders/{id}", h.getOrder)
	api.HandleFunc("POST /debug/chaos", h.chaos)

	// Order matters, outermost first:
	//   trace  - so every later layer can attach to the span
	//   metrics - so even panics and rejected requests are counted
	//   log    - so the line carries the trace id and the final status
	mux.Handle("/", h.traceMiddleware(h.metricsMiddleware(h.logMiddleware(api))))

	return mux
}

func (h *Handler) traceMiddleware(next http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		ctx, span := h.observability.Tracer.Start(ctx, r.Method+" "+routeOf(r),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			))
		defer span.End()

		w.Header().Set("X-Trace-Id", observability.TraceID(ctx))

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.response.status_code", recorder.status))
	})
}

func (h *Handler) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		h.observability.Metrics.RequestsInFlight.Inc()
		defer h.observability.Metrics.RequestsInFlight.Dec()

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		route := routeOf(r)

		h.observability.Metrics.RequestsTotal.
			WithLabelValues(r.Method, route, observability.StatusClass(recorder.status)).Inc()
		h.observability.Metrics.RequestDuration.
			WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

func (h *Handler) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		logger := h.observability.Logger.With(
			slog.String("trace_id", observability.TraceID(r.Context())),
			slog.String("method", r.Method),
			slog.String("route", routeOf(r)),
			slog.Int("status", recorder.status),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
		)

		switch {
		case recorder.status >= 500:
			logger.ErrorContext(r.Context(), "request failed")
		case recorder.status >= 400:
			logger.WarnContext(r.Context(), "request rejected")
		default:
			logger.InfoContext(r.Context(), "request completed")
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// routeOf returns the matched pattern, which is bounded, rather than the raw
// path, which is not.
func routeOf(r *http.Request) string {
	if r.Pattern != "" {
		if _, path, found := strings.Cut(r.Pattern, " "); found {
			return path
		}

		return r.Pattern
	}

	return "unmatched"
}

//
// HANDLERS
//

// health answers "is this process alive?" - it must not touch a dependency,
// or a database blip restarts every pod.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready answers "should this process receive traffic?" - that one DOES depend
// on the dependencies, and it is what a load balancer should poll.
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	state := h.service.BreakerState()

	if state == "open" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":  "degraded",
			"breaker": state,
		})

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "breaker": state})
}

func (h *Handler) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Customer string `json:"customer"`
		Cents    int64  `json:"cents"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	order, err := h.service.Create(r.Context(), input.Customer, input.Cents)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       order.ID,
		"status":   order.Status,
		"trace_id": observability.TraceID(r.Context()),
	})
}

func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	order, err := h.service.ByID(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": order.ID, "customer": order.Customer, "cents": order.Cents, "status": order.Status,
	})
}

// chaos is the failure injection endpoint: it exists so the telemetry can be
// observed under failure without waiting for a real incident.
func (h *Handler) chaos(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	set := func(name string, target interface{ Store(uint64) }) uint64 {
		value, err := strconv.ParseUint(query.Get(name), 10, 64)
		if err != nil || value > 100 {
			value = 0
		}

		target.Store(value)

		return value
	}

	database := set("database_failure_rate", &h.service.Chaos.DatabaseFailureRate)
	payment := set("payment_failure_rate", &h.service.Chaos.PaymentFailureRate)
	slow := set("slow_rate", &h.service.Chaos.SlowRate)

	h.observability.Logger.Warn("failure injection changed",
		slog.Uint64("database_failure_rate", database),
		slog.Uint64("payment_failure_rate", payment),
		slog.Uint64("slow_rate", slow))

	writeJSON(w, http.StatusOK, map[string]uint64{
		"database_failure_rate": database,
		"payment_failure_rate":  payment,
		"slow_rate":             slow,
	})
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orders.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "invalid order: "))

	case errors.Is(err, orders.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, resilience.ErrBreakerOpen):
		// 503 with Retry-After: the honest answer when a dependency is down
		// is "not now, try again shortly", not a 500.
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "payments unavailable, please retry")

	case errors.Is(err, orders.ErrDependency):
		writeError(w, http.StatusBadGateway, "payments failed")

	case errors.Is(err, context.DeadlineExceeded):
		// 504, not 500: the request was fine, a dependency did not answer in
		// time. The distinction matters on a dashboard - 5xx is "we are
		// broken", 504 is "something we depend on is".
		writeError(w, http.StatusGatewayTimeout, "payments timed out")

	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", slog.String("error", err.Error()))
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
