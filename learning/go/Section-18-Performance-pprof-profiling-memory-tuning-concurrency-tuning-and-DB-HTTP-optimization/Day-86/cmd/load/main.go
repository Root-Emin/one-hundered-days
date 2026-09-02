// Command load generates traffic against the server so there is something to
// profile.
//
//	go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/cmd/load -url 'http://127.0.0.1:8086/report?items=500&mode=slow' -workers 8 -duration 15s
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/load"
)

func main() {
	var (
		url      = flag.String("url", "http://127.0.0.1:8086/report?items=500&mode=slow", "target url")
		workers  = flag.Int("workers", 8, "concurrent clients")
		duration = flag.Duration("duration", 15*time.Second, "how long to run")
	)

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Printf("load: %s workers=%d duration=%s\n", *url, *workers, *duration)

	result := load.Run(ctx, *url, *workers, *duration)

	fmt.Println(result)

	if result.Errors > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d requests failed\n", result.Errors)
	}
}
