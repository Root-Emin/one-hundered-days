// Command server runs the fully instrumented MVP.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/httpapi"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/observability"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/orders"
)

/*
Day 75 - Observability & Resilience: Practice

Section 15 capstone. See docs/DASHBOARD.md and docs/RUNBOOK.md.

Tasks covered:

 1. The MVP emits structured logs, Prometheus metrics and OpenTelemetry
    traces, all sharing a trace id
 2. Failure injection (/debug/chaos) so the telemetry can be watched under
    failure instead of guessed at
 3. A dashboard sketch: which panels, in which order, and why (docs/)
 4. A runbook: what an on-call engineer does when the error rate spikes

Run:

	go run ./cmd/server
	go run ./cmd/loadgen             # traffic, then failure, then recovery

	curl localhost:8080/healthz      # liveness: no dependencies
	curl localhost:8080/readyz       # readiness: reports the breaker
	curl -s localhost:8080/metrics | grep day75_

Environment variables:

	PORT              Default: 8080
	LOG_FORMAT        text | json    Default: text
	LOG_LEVEL         debug|info|warn|error
	OTEL_PRINT_SPANS  true prints spans to stdout
	OTEL_SAMPLE_RATIO Default: 1
*/

func main() {
	ctx := context.Background()

	obs, err := observability.Setup(ctx, observability.ConfigFromEnv())
	if err != nil {
		slog.Error("observability setup failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.SetDefault(obs.Logger)

	if err := run(ctx, obs); err != nil {
		obs.Logger.Error("server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, obs *observability.Observability) error {
	service := orders.NewService(obs)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           httpapi.New(obs, service).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(obs.Logger.Handler(), slog.LevelError),
	}

	serverErrors := make(chan error, 1)

	go func() {
		obs.Logger.Info("server starting",
			slog.String("addr", server.Addr),
			slog.String("metrics", server.Addr+"/metrics"))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err

	case received := <-shutdown:
		obs.Logger.Info("shutdown signal", slog.String("signal", received.String()))
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		obs.Logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
	}

	// Flush the traces LAST: the spans of the final requests are exactly the
	// ones an incident review wants.
	if err := obs.Shutdown(shutdownCtx); err != nil {
		return err
	}

	obs.Logger.Info("stopped cleanly", slog.Int("orders_processed", service.Count()))

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
