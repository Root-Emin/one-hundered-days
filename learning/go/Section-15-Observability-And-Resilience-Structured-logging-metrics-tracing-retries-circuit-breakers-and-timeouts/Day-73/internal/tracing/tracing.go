// Package tracing initialises OpenTelemetry and holds the helpers the rest of
// the service uses to create spans.
//
// A trace is one request's journey; a span is one timed step of it. Together
// they answer the question logs and metrics cannot: not "was it slow?" (a
// metric) or "what happened?" (a log), but "where did the time go, across
// which services, for THIS request?"
package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// Config keeps the knobs that differ between development and production.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string

	// SampleRatio between 0 and 1. Production services trace a fraction of
	// requests: at scale, tracing everything costs more than the service.
	SampleRatio float64

	// Pretty prints readable spans to stdout. Useful while learning, far too
	// verbose for a real deployment - there, the exporter is OTLP.
	Pretty bool
}

func ConfigFromEnv() Config {
	ratio := 1.0

	if value := os.Getenv("OTEL_SAMPLE_RATIO"); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			ratio = parsed
		}
	}

	return Config{
		ServiceName:    envOr("OTEL_SERVICE_NAME", "day73"),
		ServiceVersion: envOr("SERVICE_VERSION", "dev"),
		Environment:    envOr("ENV", "development"),
		SampleRatio:    ratio,
		Pretty:         !strings.EqualFold(os.Getenv("OTEL_PRETTY"), "false"),
	}
}

// Setup wires the SDK and returns a shutdown function.
//
// The returned function MUST be called before the process exits: spans are
// batched, and an unflushed batch is a trace that never existed.
func Setup(ctx context.Context, config Config) (trace.Tracer, func(context.Context) error, error) {
	options := []stdouttrace.Option{}

	if config.Pretty {
		options = append(options, stdouttrace.WithPrettyPrint())
	}

	// In production this is an OTLP exporter pointed at a collector:
	//
	//	otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint("collector:4317"))
	//
	// The rest of this function does not change - which is the point of the
	// vendor-neutral SDK.
	exporter, err := stdouttrace.New(options...)
	if err != nil {
		return nil, nil, fmt.Errorf("create exporter: %w", err)
	}

	// The resource describes WHO is emitting the spans. Without it, a trace
	// spanning three services cannot say which service each span came from.
	//
	// The semconv version must match the one resource.Default() carries, or
	// Merge fails with "conflicting Schema URL" - a real and confusing error
	// the first time you meet it.
	attributes, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
			semconv.DeploymentEnvironmentNameKey.String(config.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		// Batching, not one export per span: an exporter call on the request
		// path would add its latency to every request.
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(time.Second),
			sdktrace.WithMaxExportBatchSize(64),
		),
		sdktrace.WithResource(attributes),
		// ParentBased means a sampling decision made upstream is respected:
		// either the whole distributed trace is sampled or none of it is. A
		// per-service decision produces traces with holes in them.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
	)

	otel.SetTracerProvider(provider)

	// The propagator decides the wire format of trace context. W3C
	// traceparent is the standard; Baggage carries user-defined key/values
	// alongside it.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider.Tracer(config.ServiceName), provider.Shutdown, nil
}

// Tracer returns a named tracer from the global provider. Naming it after the
// package makes a span's origin obvious in a viewer.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Start opens a span and returns it with the derived context.
//
// The context is the trace: passing the ORIGINAL ctx to the next call instead
// of the returned one silently detaches everything below it from the trace.
func Start(ctx context.Context, tracer trace.Tracer, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attributes...))
}

// RecordError marks a span as failed and attaches the error.
//
// Both calls are needed: RecordError adds an event with the message,
// SetStatus is what makes the span show up as an error in a viewer.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// TraceIDFrom returns the current trace id, for putting into a log line.
//
// This is the join between the three pillars: a log line that carries the
// trace id lets an engineer jump from "this request failed" to "here is where
// its time went".
func TraceIDFrom(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)

	if !spanContext.HasTraceID() {
		return ""
	}

	return spanContext.TraceID().String()
}

func SpanIDFrom(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)

	if !spanContext.HasSpanID() {
		return ""
	}

	return spanContext.SpanID().String()
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
