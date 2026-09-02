// Package perf is the measuring apparatus: a load generator, latency
// percentiles, and the regression budgets that keep a win from quietly
// leaking away.
package perf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Result is one load run.
type Result struct {
	Label      string
	Requests   int
	Errors     int
	Wall       time.Duration
	Throughput float64

	Mean time.Duration
	P50  time.Duration
	P95  time.Duration
	P99  time.Duration
	Max  time.Duration
}

func (r Result) String() string {
	return fmt.Sprintf("%s: n=%d errors=%d p50=%s p95=%s p99=%s %.0f req/s",
		r.Label, r.Requests, r.Errors,
		r.P50.Round(time.Microsecond), r.P95.Round(time.Microsecond),
		r.P99.Round(time.Microsecond), r.Throughput)
}

// Options describe a load run.
type Options struct {
	Label    string
	URL      string
	Workers  int
	Requests int // total across all workers
}

// Run drives `Requests` requests through `Workers` concurrent clients.
//
// A fixed request COUNT rather than a fixed duration: comparing two versions
// needs the same amount of work, not the same amount of time.
func Run(ctx context.Context, client *http.Client, options Options) (Result, error) {
	if options.Workers < 1 {
		options.Workers = 1
	}

	if options.Requests < 1 {
		options.Requests = 1
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		latencies = make([]time.Duration, 0, options.Requests)
		errors    int
	)

	perWorker := options.Requests / options.Workers
	remainder := options.Requests % options.Workers

	start := time.Now()

	for worker := 0; worker < options.Workers; worker++ {
		count := perWorker

		if worker < remainder {
			count++
		}

		wg.Add(1)

		go func(count int) {
			defer wg.Done()

			local := make([]time.Duration, 0, count)
			localErrors := 0

			for i := 0; i < count; i++ {
				requestStart := time.Now()

				if err := fetch(ctx, client, options.URL); err != nil {
					localErrors++

					continue
				}

				local = append(local, time.Since(requestStart))
			}

			mu.Lock()
			latencies = append(latencies, local...)
			errors += localErrors
			mu.Unlock()
		}(count)
	}

	wg.Wait()

	wall := time.Since(start)

	if len(latencies) == 0 {
		return Result{Label: options.Label, Errors: errors, Wall: wall},
			fmt.Errorf("%s: every request failed", options.Label)
	}

	return summarise(options.Label, latencies, errors, wall), nil
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
	// pool, and then the load test measures TCP handshakes.
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", response.Status)
	}

	return nil
}

func summarise(label string, latencies []time.Duration, errorCount int, wall time.Duration) Result {
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)

	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration

	for _, sample := range sorted {
		total += sample
	}

	return Result{
		Label:      label,
		Requests:   len(sorted),
		Errors:     errorCount,
		Wall:       wall,
		Throughput: float64(len(sorted)) / wall.Seconds(),
		Mean:       total / time.Duration(len(sorted)),
		P50:        percentile(sorted, 0.50),
		P95:        percentile(sorted, 0.95),
		P99:        percentile(sorted, 0.99),
		Max:        sorted[len(sorted)-1],
	}
}

// percentile uses nearest-rank on a sorted slice.
//
// The mean hides the tail, and the tail is what users notice: a p99 of two
// seconds means one request in a hundred is unusable, however good the average
// looks.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	index := int(float64(len(sorted))*fraction) - 1

	if index < 0 {
		index = 0
	}

	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	return sorted[index]
}
