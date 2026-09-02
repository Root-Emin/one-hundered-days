// Command demo runs both services in one process and sends a few requests
// through them, so the exported spans show a real distributed trace.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/orders"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/payments"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

/*
Day 73 - Observability & Resilience: Tracing and OpenTelemetry

Tasks covered:

 1. The OTel SDK initialised with a resource, a sampler and a batching
    exporter (internal/tracing)
 2. Spans around the HTTP handler, the repository calls and the outbound
    payment call, with attributes, events and error status
 3. Trace context propagated over HTTP: Inject on the way out, Extract on the
    way in - the two lines that make one trace instead of two
 4. Exported to the console for learning; one line changes that to OTLP

Run:

	go run ./cmd/demo               # pretty spans on stdout
	OTEL_PRETTY=false go run ./cmd/demo
	OTEL_SAMPLE_RATIO=0.1 go run ./cmd/demo   # sample 10% of traces

Sending to a real collector instead of stdout (docker compose):

	services:
	  jaeger:
	    image: jaegertracing/all-in-one:1.60
	    ports: ["16686:16686", "4317:4317"]

	OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 go run ./cmd/demo
	# then open http://localhost:16686

	(swap stdouttrace.New for otlptracegrpc.New in internal/tracing; the rest
	of the setup is identical - that is the point of the vendor-neutral SDK.)

Test:

	go test ./...
*/

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("demo failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	_, shutdown, err := tracing.Setup(ctx, tracing.ConfigFromEnv())
	if err != nil {
		return err
	}

	// Spans are batched: without this flush the process can exit with traces
	// still in memory, and they are simply lost.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := shutdown(shutdownCtx); err != nil {
			logger.Error("tracer shutdown", slog.String("error", err.Error()))
		}
	}()

	// The payments service, on its own listener.
	paymentsServer := httptest.NewServer(payments.NewServer().Routes())
	defer paymentsServer.Close()

	// The orders service, which calls it.
	service := orders.NewService(
		orders.NewRepository(),
		orders.NewPaymentsClient(paymentsServer.URL, &http.Client{Timeout: 3 * time.Second}),
		logger,
	)

	ordersServer := httptest.NewServer(service.Routes())
	defer ordersServer.Close()

	fmt.Println("\nSending requests through orders -> payments")
	fmt.Println(strings.Repeat("-", 78))

	requests := []struct {
		label    string
		customer string
		cents    int64
	}{
		{"normal order", "ada", 4999},
		{"another order", "alan", 12900},
		{"over the payment limit", "grace", 250_000},
	}

	for _, request := range requests {
		traceID, status, err := createOrder(ctx, ordersServer.URL, request.customer, request.cents)
		if err != nil {
			return err
		}

		fmt.Printf("  %-24s status=%d trace_id=%s\n", request.label, status, traceID)
	}

	fmt.Println("\n  Each trace_id above appears in the spans printed below, in the log")
	fmt.Println("  lines, and in the X-Trace-Id response header. One id ties them")
	fmt.Println("  together - that is the whole trick.")

	fmt.Println("\nWhat to look for in the exported spans")
	fmt.Println(strings.Repeat("-", 78))
	fmt.Println("  * every span shares one TraceID, with different SpanIDs")
	fmt.Println("  * the payments span's Parent is the orders client span, ACROSS the")
	fmt.Println("    process boundary - that is Inject/Extract working")
	fmt.Println("  * the failed order's span carries Status: Error and an exception event")
	fmt.Println("  * db.operation attributes group the repository spans in a viewer")
	fmt.Println()

	// Give the batcher a moment so the spans print before the summary above
	// scrolls away.
	time.Sleep(1500 * time.Millisecond)

	return nil
}

func createOrder(ctx context.Context, baseURL, customer string, cents int64) (string, int, error) {
	payload, err := json.Marshal(map[string]any{"customer": customer, "cents": cents})
	if err != nil {
		return "", 0, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost,
		baseURL+"/orders", strings.NewReader(string(payload)))
	if err != nil {
		return "", 0, err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", 0, err
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			slog.Warn("close response", slog.String("error", err.Error()))
		}
	}()

	return response.Header.Get("X-Trace-Id"), response.StatusCode, nil
}
