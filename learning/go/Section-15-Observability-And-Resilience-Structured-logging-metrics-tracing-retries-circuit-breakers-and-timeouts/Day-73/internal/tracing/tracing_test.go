package tracing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/orders"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/payments"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

/*
Tracing tests use tracetest.SpanRecorder: an in-memory exporter that keeps
every finished span, so a test can assert on the trace instead of eyeballing
console output.

The assertions that matter are structural - which span is whose parent, does
the trace survive an HTTP hop, is a failure marked as an error - because those
are exactly the things that break silently.
*/

// setupRecorder installs a recording provider globally and restores the
// previous one afterwards.
func setupRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown provider: %v", err)
		}

		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	return recorder
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))

	for _, span := range spans {
		names = append(names, span.Name())
	}

	return names
}

func findSpan(spans []sdktrace.ReadOnlySpan, name string) (sdktrace.ReadOnlySpan, bool) {
	for _, span := range spans {
		if span.Name() == name {
			return span, true
		}
	}

	return nil, false
}

func TestSpansNest(t *testing.T) {
	recorder := setupRecorder(t)

	tracer := tracing.Tracer("test")

	parentCtx, parent := tracer.Start(context.Background(), "parent")

	// The CHILD gets the context returned by the parent's Start. Using the
	// original context here is the mistake that produces flat, useless traces.
	_, child := tracer.Start(parentCtx, "child")

	child.End()
	parent.End()

	spans := recorder.Ended()

	if len(spans) != 2 {
		t.Fatalf("spans = %v, want parent and child", spanNames(spans))
	}

	childSpan, ok := findSpan(spans, "child")
	if !ok {
		t.Fatal("no child span")
	}

	parentSpan, ok := findSpan(spans, "parent")
	if !ok {
		t.Fatal("no parent span")
	}

	if childSpan.Parent().SpanID() != parentSpan.SpanContext().SpanID() {
		t.Fatal("the child is not attached to the parent")
	}

	if childSpan.SpanContext().TraceID() != parentSpan.SpanContext().TraceID() {
		t.Fatal("parent and child are in different traces")
	}
}

func TestRecordErrorMarksTheSpan(t *testing.T) {
	recorder := setupRecorder(t)

	_, span := tracing.Tracer("test").Start(context.Background(), "failing")

	tracing.RecordError(span, errors.New("everything is on fire"))

	span.End()

	spans := recorder.Ended()

	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}

	// Both halves matter: the status is what a viewer colours red, the event
	// is what carries the message.
	if spans[0].Status().Code != codes.Error {
		t.Fatalf("status = %v, want Error", spans[0].Status().Code)
	}

	if len(spans[0].Events()) == 0 {
		t.Fatal("the error was not recorded as an event")
	}
}

func TestRecordErrorIgnoresNil(t *testing.T) {
	recorder := setupRecorder(t)

	_, span := tracing.Tracer("test").Start(context.Background(), "fine")

	tracing.RecordError(span, nil)

	span.End()

	if code := recorder.Ended()[0].Status().Code; code == codes.Error {
		t.Fatal("a nil error marked the span as failed")
	}
}

func TestTraceIDIsReadable(t *testing.T) {
	setupRecorder(t)

	ctx, span := tracing.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	traceID := tracing.TraceIDFrom(ctx)

	if len(traceID) != 32 {
		t.Fatalf("trace id = %q, want 32 hex characters", traceID)
	}

	if tracing.SpanIDFrom(ctx) == "" {
		t.Fatal("span id is empty")
	}

	// Outside a span there is nothing to report, and that must not panic.
	if tracing.TraceIDFrom(context.Background()) != "" {
		t.Fatal("a context without a span reported a trace id")
	}
}

// TestTraceCrossesTheNetwork is the test that protects the Inject/Extract
// pair: two services, one trace.
func TestTraceCrossesTheNetwork(t *testing.T) {
	recorder := setupRecorder(t)

	paymentsServer := httptest.NewServer(payments.NewServer().Routes())
	t.Cleanup(paymentsServer.Close)

	service := orders.NewService(
		orders.NewRepository(),
		orders.NewPaymentsClient(paymentsServer.URL, paymentsServer.Client()),
		nil,
	)

	ordersServer := httptest.NewServer(service.Routes())
	t.Cleanup(ordersServer.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		ordersServer.URL+"/orders", strings.NewReader(`{"customer":"ada","cents":4999}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := ordersServer.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var body struct {
		TraceID string `json:"trace_id"`
	}

	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	spans := recorder.Ended()

	if len(spans) < 5 {
		t.Fatalf("spans = %v, want the server, use case, repository and payment spans", spanNames(spans))
	}

	// Every span must belong to ONE trace, including the ones created in the
	// payments service after an HTTP hop.
	var traceID trace.TraceID

	for _, span := range spans {
		if !traceID.IsValid() {
			traceID = span.SpanContext().TraceID()

			continue
		}

		if span.SpanContext().TraceID() != traceID {
			t.Fatalf("span %q is in a different trace: propagation is broken", span.Name())
		}
	}

	if traceID.String() != body.TraceID {
		t.Fatalf("response trace id = %q, spans carry %q", body.TraceID, traceID)
	}

	// The payments span exists and its parent is the orders client span.
	paymentSpan, ok := findSpan(spans, "payments.authorize")
	if !ok {
		t.Fatalf("no payments span: %v", spanNames(spans))
	}

	if !paymentSpan.Parent().IsValid() {
		t.Fatal("the payments span has no parent: it started a new trace")
	}

	// And the response header carries the same id, for support to search by.
	if header := response.Header.Get("X-Trace-Id"); header != body.TraceID {
		t.Fatalf("X-Trace-Id = %q, body says %q", header, body.TraceID)
	}
}

// TestFailedPaymentIsVisibleInTheTrace: an error in one service must be
// visible when reading the trace, not only in that service's logs.
func TestFailedPaymentIsVisibleInTheTrace(t *testing.T) {
	recorder := setupRecorder(t)

	paymentsServer := httptest.NewServer(payments.NewServer().Routes())
	t.Cleanup(paymentsServer.Close)

	service := orders.NewService(
		orders.NewRepository(),
		orders.NewPaymentsClient(paymentsServer.URL, paymentsServer.Client()),
		nil,
	)

	ordersServer := httptest.NewServer(service.Routes())
	t.Cleanup(ordersServer.Close)

	// Over the per-transaction limit: the payments service declines.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		ordersServer.URL+"/orders", strings.NewReader(`{"customer":"grace","cents":250000}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := ordersServer.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}

	if response.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", response.StatusCode)
	}

	errorSpans := 0

	for _, span := range recorder.Ended() {
		if span.Status().Code == codes.Error {
			errorSpans++
		}
	}

	// The payment span, the client span and the use case span should all be
	// marked failed, so the trace shows where the failure originated.
	if errorSpans < 2 {
		t.Fatalf("%d spans marked as errors, want the failure visible along the chain", errorSpans)
	}
}

func TestSetupReturnsAWorkingTracer(t *testing.T) {
	ctx := context.Background()

	tracer, shutdown, err := tracing.Setup(ctx, tracing.Config{
		ServiceName: "test-service",
		SampleRatio: 1,
		Pretty:      false,
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, span := tracer.Start(ctx, "smoke")
	span.End()

	// The shutdown flushes: skipping it in main loses whatever is batched.
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
