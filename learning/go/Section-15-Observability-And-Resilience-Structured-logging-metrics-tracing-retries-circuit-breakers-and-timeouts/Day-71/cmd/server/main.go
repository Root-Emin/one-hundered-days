// Command server runs the demo API with structured logging.
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

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-71/internal/api"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-71/internal/logging"
)

/*
Day 71 - Observability & Resilience: Structured Logging

Tasks covered:

 1. log/slog instead of fmt.Println: JSON in production, text in a terminal
 2. Every request line carries request_id, method, path, status and duration
 3. Levels chosen by outcome: 5xx error, 4xx warn, normal info, noise debug
 4. Secrets and PII never reach the sink: a ReplaceAttr denylist plus email
    masking, on top of simply not logging them

Files:

	internal/logging  handler setup, redaction, request-scoped logger
	internal/api      the middleware and the level guidance in practice

Run:

	go run ./cmd/server                        # JSON logs
	LOG_FORMAT=text go run ./cmd/server        # readable in a terminal
	LOG_LEVEL=debug LOG_FORMAT=text go run ./cmd/server

	curl localhost:8080/healthz
	curl -XPOST localhost:8080/orders -d '{"customer":"ada","email":"ada@example.com","cents":1299,"password":"hunter2"}'
	curl localhost:8080/orders/1
	curl localhost:8080/orders/999                       # 404 -> warn
	curl -XPOST localhost:8080/debug/fail-next           # arm a failure
	curl -XPOST localhost:8080/orders -d '{"customer":"ada","cents":100}'   # 500 -> error

	curl -H 'X-Request-ID: my-trace-id' localhost:8080/healthz   # id is honoured

Environment variables:

	LOG_FORMAT   json | text     Default: json
	LOG_LEVEL    debug|info|warn|error   Default: info
	SERVICE_NAME appears on every line.  Default: day71
	PORT                                  Default: 8080

Test:

	go test ./...
*/

func main() {
	logger := logging.New()

	// Make the standard logger use the same handler, so a library that calls
	// log.Printf does not write unstructured lines into a structured stream.
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server stopped with an error", slog.String(logging.FieldError, err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	store := api.NewStore()

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           api.New(logger, store).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		// net/http writes its own errors to a *log.Logger; route them into
		// slog so they are structured too.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErrors := make(chan error, 1)

	go func() {
		logger.Info("server starting", slog.String("addr", server.Addr))

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
		logger.Info("shutdown signal received", slog.String("signal", received.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return err
	}

	logger.Info("server stopped cleanly")

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
