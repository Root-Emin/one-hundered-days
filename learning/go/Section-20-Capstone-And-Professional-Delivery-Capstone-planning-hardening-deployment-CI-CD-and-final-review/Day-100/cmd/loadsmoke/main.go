// Command loadsmoke puts modest load on a running Linkr and reports the
// percentiles the requirements promised.
//
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/cmd/loadsmoke -url http://127.0.0.1:8098/golang -workers 16 -requests 5000
//
// "Smoke" rather than "load": the point is not to find the breaking point, it
// is to check that the numbers the requirements committed to are real before
// anyone else reads them.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"
)

func main() {
	var (
		target   = flag.String("url", "http://127.0.0.1:8098/golang", "url to hammer")
		workers  = flag.Int("workers", 16, "concurrent clients")
		requests = flag.Int("requests", 5000, "total requests")
		slo      = flag.Duration("slo-p95", 5*time.Millisecond, "the p95 the requirements promise")
	)

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Enough idle connections for every worker, or the test measures its own
	// TCP handshakes rather than the server.
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *workers * 2,
			MaxIdleConnsPerHost: *workers * 2,
			IdleConnTimeout:     30 * time.Second,
		},
		// A redirect is the thing being measured, not something to follow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	fmt.Printf("%d requests, %d workers -> %s\n\n", *requests, *workers, *target)

	result := run(ctx, client, *target, *workers, *requests)

	fmt.Printf("requests   %d (%d failed)\n", result.count, result.errors)
	fmt.Printf("throughput %.0f req/s\n", float64(result.count)/result.wall.Seconds())
	fmt.Printf("p50        %s\n", result.p50.Round(time.Microsecond))
	fmt.Printf("p95        %s\n", result.p95.Round(time.Microsecond))
	fmt.Printf("p99        %s\n", result.p99.Round(time.Microsecond))
	fmt.Printf("max        %s\n", result.max.Round(time.Microsecond))

	if result.errors > 0 {
		fmt.Fprintf(os.Stderr, "\n%d requests failed\n", result.errors)
		os.Exit(1)
	}

	fmt.Printf("\nSLO p95 < %s: ", *slo)

	if result.p95 > *slo {
		fmt.Println("MISSED")
		os.Exit(1)
	}

	fmt.Println("met")
}

type summary struct {
	count, errors int
	wall          time.Duration
	p50, p95, p99 time.Duration
	max           time.Duration
}

func run(ctx context.Context, client *http.Client, url string, workers, requests int) summary {
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		latencies = make([]time.Duration, 0, requests)
		failures  int
	)

	perWorker := requests / workers

	start := time.Now()

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			local := make([]time.Duration, 0, perWorker)
			localFailures := 0

			for i := 0; i < perWorker; i++ {
				if ctx.Err() != nil {
					break
				}

				requestStart := time.Now()

				if err := fetch(ctx, client, url); err != nil {
					localFailures++

					continue
				}

				local = append(local, time.Since(requestStart))
			}

			mu.Lock()
			latencies = append(latencies, local...)
			failures += localFailures
			mu.Unlock()
		}()
	}

	wg.Wait()

	wall := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	result := summary{count: len(latencies), errors: failures, wall: wall}

	if len(latencies) == 0 {
		return result
	}

	result.p50 = percentile(latencies, 0.50)
	result.p95 = percentile(latencies, 0.95)
	result.p99 = percentile(latencies, 0.99)
	result.max = latencies[len(latencies)-1]

	return result
}

func fetch(ctx context.Context, client *http.Client, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			_ = err
		}
	}()

	// Drain the body: an unread body keeps the connection out of the idle
	// pool, and then the test measures TCP handshakes.
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}

	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", response.Status)
	}

	return nil
}

// percentile uses nearest-rank on a sorted slice.
//
// The mean is not reported anywhere: it hides the tail, and the tail is what
// users feel.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	index := int(float64(len(sorted))*fraction) - 1

	if index < 0 {
		index = 0
	}

	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return sorted[index]
}
