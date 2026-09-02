// Package payments is a second service, so trace context has a network to
// cross.
package payments

import (
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

type Server struct {
	tracer trace.Tracer
}

func NewServer() *Server {
	return &Server{tracer: tracing.Tracer("payments")}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /charges", s.charge)

	// The extraction middleware is what continues the caller's trace instead
	// of starting a new one.
	return ExtractTrace(mux, s.tracer)
}

func (s *Server) charge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var input struct {
		OrderID int64 `json:"order_id"`
		Cents   int64 `json:"cents"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// A child span for the work this service actually does. It is a child of
	// the ORDER service's span, in another process, because the trace context
	// travelled in a header.
	ctx, span := s.tracer.Start(ctx, "payments.authorize",
		trace.WithAttributes(
			attribute.Int64("order.id", input.OrderID),
			attribute.Int64("payment.amount_cents", input.Cents),
		))
	defer span.End()

	// Simulated latency, occasionally slow: exactly the kind of thing tracing
	// exists to attribute to the right service.
	//nolint:gosec // load shaping, not security
	delay := time.Duration(5+rand.IntN(20)) * time.Millisecond

	if rand.Float64() < 0.2 {
		delay = time.Duration(120+rand.IntN(120)) * time.Millisecond

		span.AddEvent("slow path taken", trace.WithAttributes(
			attribute.String("reason", "issuer network latency")))
	}

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		// The caller's deadline reached this service through the context.
		tracing.RecordError(span, ctx.Err())
		http.Error(w, "cancelled", http.StatusRequestTimeout)

		return
	}

	if input.Cents > 100_000 {
		err := errors.New("amount exceeds the per-transaction limit")

		tracing.RecordError(span, err)
		span.SetAttributes(attribute.String("payment.decline_reason", "limit_exceeded"))

		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	span.SetAttributes(attribute.String("payment.status", "authorized"))

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(map[string]any{
		"authorized": true,
		"trace_id":   tracing.TraceIDFrom(ctx),
	}); err != nil {
		log.Printf("encode response: %v", err)
	}
}
