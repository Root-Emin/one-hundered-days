package orders

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

type Service struct {
	repository *Repository
	payments   *PaymentsClient
	tracer     trace.Tracer
	logger     *slog.Logger
}

func NewService(repository *Repository, payments *PaymentsClient, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		repository: repository,
		payments:   payments,
		tracer:     tracing.Tracer("orders"),
		logger:     logger,
	}
}

func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /orders", s.createOrder)

	return s.traceMiddleware(mux)
}

func (s *Service) traceMiddleware(next http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		ctx, span := s.tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			))
		defer span.End()

		// Returning the trace id lets a caller (or a support engineer) find
		// the trace for the request they are complaining about.
		w.Header().Set("X-Trace-Id", tracing.TraceIDFrom(ctx))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) createOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Logs carry the trace id: this is how an engineer jumps from a log line
	// to the trace that explains it.
	logger := s.logger.With(
		slog.String("trace_id", tracing.TraceIDFrom(ctx)),
		slog.String("span_id", tracing.SpanIDFrom(ctx)),
	)

	var input struct {
		Customer string `json:"customer"`
		Cents    int64  `json:"cents"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// A span for the business operation, so the trace shows the shape of the
	// use case rather than a flat list of queries.
	ctx, span := s.tracer.Start(ctx, "CreateOrder",
		trace.WithAttributes(attribute.String("customer", input.Customer)))
	defer span.End()

	order, err := s.repository.Save(ctx, Order{Customer: input.Customer, Cents: input.Cents})
	if err != nil {
		tracing.RecordError(span, err)
		logger.ErrorContext(ctx, "save order failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	span.SetAttributes(attribute.Int64("order.id", order.ID))
	span.AddEvent("order persisted")

	if err := s.payments.Charge(ctx, order.ID, order.Cents); err != nil {
		tracing.RecordError(span, err)

		if updateErr := s.repository.UpdateStatus(ctx, order.ID, "payment_failed"); updateErr != nil {
			logger.ErrorContext(ctx, "status update failed", slog.String("error", updateErr.Error()))
		}

		logger.WarnContext(ctx, "payment failed",
			slog.Int64("order_id", order.ID), slog.String("error", err.Error()))

		http.Error(w, "payment failed", http.StatusPaymentRequired)

		return
	}

	if err := s.repository.UpdateStatus(ctx, order.ID, "paid"); err != nil {
		tracing.RecordError(span, err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	span.AddEvent("order paid")

	logger.InfoContext(ctx, "order created", slog.Int64("order_id", order.ID))

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(map[string]any{
		"id":       order.ID,
		"status":   "paid",
		"trace_id": tracing.TraceIDFrom(ctx),
	}); err != nil {
		logger.ErrorContext(ctx, "encode response", slog.String("error", err.Error()))
	}
}
