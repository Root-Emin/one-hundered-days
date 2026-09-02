// Package observability wires the three pillars together and hands the rest
// of the service one object to use.
//
// Logs, metrics and traces answer different questions:
//
//	metrics  how many, how fast, how full     -> is something wrong?
//	traces   where did the time go, for one   -> where is it wrong?
//	logs     what exactly happened, in detail -> why is it wrong?
//
// They are only useful together, and only if they share identifiers. Every
// log line here carries trace_id; every trace carries the request id; the
// metrics carry the route. That is what turns three tools into one workflow.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"net/http"
)

type Config struct {
	ServiceName string
	Environment string
	LogLevel    slog.Level
	LogFormat   string
	SampleRatio float64
	TraceOutput bool
}

func ConfigFromEnv() Config {
	level := slog.LevelInfo

	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	ratio := 1.0

	if value := os.Getenv("OTEL_SAMPLE_RATIO"); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			ratio = parsed
		}
	}

	return Config{
		ServiceName: envOr("SERVICE_NAME", "day75"),
		Environment: envOr("ENV", "development"),
		LogLevel:    level,
		LogFormat:   envOr("LOG_FORMAT", "text"),
		SampleRatio: ratio,
		// Printing every span drowns the demo output; off by default here,
		// on when someone wants to see them.
		TraceOutput: strings.EqualFold(os.Getenv("OTEL_PRINT_SPANS"), "true"),
	}
}

type Observability struct {
	Logger  *slog.Logger
	Tracer  trace.Tracer
	Metrics *Metrics

	shutdown func(context.Context) error
}

func Setup(ctx context.Context, config Config) (*Observability, error) {
	logger := newLogger(config)

	tracer, shutdown, err := newTracer(ctx, config)
	if err != nil {
		return nil, err
	}

	return &Observability{
		Logger:   logger,
		Tracer:   tracer,
		Metrics:  NewMetrics(config.ServiceName),
		shutdown: shutdown,
	}, nil
}

// Shutdown flushes the trace batch. Skipping it loses the traces of whatever
// the service was doing when it was told to stop - which is exactly the
// window an incident cares about.
func (o *Observability) Shutdown(ctx context.Context) error {
	return o.shutdown(ctx)
}

func newLogger(config Config) *slog.Logger {
	options := &slog.HandlerOptions{
		Level:       config.LogLevel,
		ReplaceAttr: redact,
	}

	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)

	if strings.EqualFold(config.LogFormat, "text") {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	return slog.New(handler).With(
		slog.String("service", config.ServiceName),
		slog.String("env", config.Environment),
	)
}

var sensitive = map[string]struct{}{
	"password": {}, "token": {}, "secret": {}, "authorization": {}, "api_key": {},
}

func redact(groups []string, attr slog.Attr) slog.Attr {
	if _, found := sensitive[strings.ToLower(attr.Key)]; found {
		return slog.String(attr.Key, "[REDACTED]")
	}

	return attr
}

func newTracer(ctx context.Context, config Config) (trace.Tracer, func(context.Context) error, error) {
	writer := os.Stdout

	options := []stdouttrace.Option{stdouttrace.WithWriter(writer)}

	if !config.TraceOutput {
		// Discard spans unless asked for: the SDK still runs, so the trace
		// ids that tie logs together are real.
		options = []stdouttrace.Option{stdouttrace.WithWriter(discard{})}
	}

	exporter, err := stdouttrace.New(options...)
	if err != nil {
		return nil, nil, fmt.Errorf("trace exporter: %w", err)
	}

	attributes, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(config.ServiceName),
		semconv.DeploymentEnvironmentNameKey.String(config.Environment),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(attributes),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	return provider.Tracer(config.ServiceName), provider.Shutdown, nil
}

type discard struct{}

func (discard) Write(data []byte) (int, error) { return len(data), nil }

//
// METRICS
//

type Metrics struct {
	Registry *prometheus.Registry

	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight prometheus.Gauge

	DependencyCalls    *prometheus.CounterVec
	DependencyDuration *prometheus.HistogramVec
	BreakerState       *prometheus.GaugeVec
	RetriesTotal       *prometheus.CounterVec

	OrdersTotal *prometheus.CounterVec
}

func NewMetrics(namespace string) *Metrics {
	registry := prometheus.NewRegistry()

	metrics := &Metrics{
		Registry: registry,

		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "http", Name: "requests_total",
			Help: "HTTP requests by route template and status class.",
		}, []string{"method", "route", "status"}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "http", Name: "request_duration_seconds",
			Help:    "HTTP latency.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}, []string{"method", "route"}),

		RequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "http", Name: "requests_in_flight",
			Help: "Requests being served right now (saturation).",
		}),

		DependencyCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dependency", Name: "calls_total",
			Help: "Outbound calls by dependency and outcome.",
		}, []string{"dependency", "outcome"}),

		DependencyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "dependency", Name: "duration_seconds",
			Help:    "Outbound call latency.",
			Buckets: []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2, 5},
		}, []string{"dependency"}),

		// A gauge per state, so a dashboard can show "how long was the
		// breaker open?" rather than only "is it open now?".
		BreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "dependency", Name: "breaker_state",
			Help: "Circuit breaker state: 0 closed, 1 half-open, 2 open.",
		}, []string{"dependency"}),

		RetriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dependency", Name: "retries_total",
			Help: "Retry attempts by dependency.",
		}, []string{"dependency"}),

		OrdersTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "orders", Name: "processed_total",
			Help: "Orders by outcome.",
		}, []string{"outcome"}),
	}

	registry.MustRegister(
		metrics.RequestsTotal, metrics.RequestDuration, metrics.RequestsInFlight,
		metrics.DependencyCalls, metrics.DependencyDuration, metrics.BreakerState,
		metrics.RetriesTotal, metrics.OrdersTotal,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return metrics
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError})
}

//
// SHARED HELPERS
//

// TraceID returns the current trace id for a log line.
func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)

	if !spanContext.HasTraceID() {
		return ""
	}

	return spanContext.TraceID().String()
}

// RecordError marks a span failed. Both calls are needed: the event carries
// the message, the status is what a viewer colours red.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func StatusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}

	return strconv.Itoa(status/100) + "xx"
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
