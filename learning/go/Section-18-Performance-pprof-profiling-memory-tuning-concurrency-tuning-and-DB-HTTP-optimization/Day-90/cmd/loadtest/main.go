// Command loadtest drives traffic at a running server and reports percentiles.
//
//	go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/cmd/loadtest -url 'http://127.0.0.1:8090/dashboard?v=1' -workers 8 -requests 500
//
// A fixed request COUNT rather than a fixed duration: comparing two versions
// needs the same amount of work, not the same amount of time.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/perf"
)

func main() {
	var (
		url      = flag.String("url", "http://127.0.0.1:8090/dashboard?v=1", "target url")
		workers  = flag.Int("workers", 8, "concurrent clients")
		requests = flag.Int("requests", 500, "total requests")
	)

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Enough idle connections for every worker, or the test measures its own
	// TCP handshakes instead of the server.
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *workers * 2,
			MaxIdleConnsPerHost: *workers * 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	result, err := perf.Run(ctx, client, perf.Options{
		Label:    *url,
		URL:      *url,
		Workers:  *workers,
		Requests: *requests,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fmt.Printf("requests   %d (%d failed)\n", result.Requests, result.Errors)
	fmt.Printf("wall       %s\n", result.Wall.Round(time.Millisecond))
	fmt.Printf("throughput %.0f req/s\n", result.Throughput)
	fmt.Printf("mean       %s\n", result.Mean.Round(time.Microsecond))
	fmt.Printf("p50        %s\n", result.P50.Round(time.Microsecond))
	fmt.Printf("p95        %s\n", result.P95.Round(time.Microsecond))
	fmt.Printf("p99        %s\n", result.P99.Round(time.Microsecond))
	fmt.Printf("max        %s\n", result.Max.Round(time.Microsecond))
}
