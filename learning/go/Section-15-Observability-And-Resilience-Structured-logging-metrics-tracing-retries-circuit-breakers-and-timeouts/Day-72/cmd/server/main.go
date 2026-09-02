// Command server exposes the instrumented API and its /metrics endpoint.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-72/internal/api"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-72/internal/metrics"
)

/*
Day 72 - Observability & Resilience: Metrics with Prometheus

Tasks covered:

 1. /metrics served from the HTTP server (and excluded from its own counters)
 2. Counters for requests and orders, histograms for latency, order value and
    response size, gauges for in-flight requests and queue depth
 3. Labels kept bounded: route TEMPLATES not raw paths, status CLASSES not
    codes, error KINDS not messages - see metrics.RouteTemplate
 4. Scrapeable locally with curl, and by Prometheus with the config below

Run:

	go run ./cmd/server

	curl -s localhost:8080/metrics | head -40
	curl -XPOST localhost:8080/orders -d '{"customer":"ada","cents":1299}'
	curl localhost:8080/orders/1
	curl localhost:8080/orders/999

	# make it interesting: 20% of database calls fail, 30% are slow
	curl -XPOST 'localhost:8080/debug/chaos?failure_rate=0.2&slow_rate=0.3'
	for i in $(seq 1 50); do curl -s -o /dev/null localhost:8080/orders; done
	curl -s localhost:8080/metrics | grep -E 'day72_(http|orders|database)'

Prometheus scrape config for a local run:

	scrape_configs:
	  - job_name: day72
	    scrape_interval: 5s
	    static_configs:
	      - targets: ['host.docker.internal:8080']

The four queries worth knowing (RED plus saturation):

	# request rate by route
	sum by (route) (rate(day72_http_requests_total[5m]))

	# error ratio
	sum(rate(day72_http_requests_total{status="5xx"}[5m]))
	  / sum(rate(day72_http_requests_total[5m]))

	# p95 latency by route
	histogram_quantile(0.95,
	  sum by (le, route) (rate(day72_http_request_duration_seconds_bucket[5m])))

	# saturation
	day72_http_requests_in_flight

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day72: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	recorder := metrics.New(envOr("METRICS_NAMESPACE", "day72"))

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           api.New(recorder).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s, metrics at %s/metrics", server.Addr, server.Addr)

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
		log.Printf("shutdown signal: %s", received)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return server.Shutdown(ctx)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
