package payments

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ExtractTrace reads the incoming trace context and starts a server span.
//
// Without the Extract call this service would start a brand new trace for
// every request, and the two halves of a distributed request would never be
// connected - the single most common tracing mistake.
//
// go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp does this (and
// more) in one line; it is written out here so the mechanism is visible.
func ExtractTrace(next http.Handler, tracer trace.Tracer) http.Handler {
	propagator := otel.GetTextMapPropagator()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HeaderCarrier adapts http.Header to the propagator's interface.
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		ctx, span := tracer.Start(ctx, "POST "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			),
		)
		defer span.End()

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.response.status_code", recorder.status))
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
