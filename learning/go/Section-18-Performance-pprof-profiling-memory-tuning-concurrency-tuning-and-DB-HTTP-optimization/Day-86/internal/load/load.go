// Package load generates traffic worth profiling.
//
// "Generate load representative of reality" is the part people skip. An idle
// server profiles as runtime.mallocgc and epoll_wait, which tells you nothing.
// The profile is only as useful as the work happening during it.
package load

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	Requests int64
	Errors   int64
	Duration time.Duration
	Mean     time.Duration
	P99      time.Duration
}

func (r Result) String() string {
	return fmt.Sprintf("requests=%d errors=%d wall=%s mean=%s p99=%s",
		r.Requests, r.Errors, r.Duration.Round(time.Millisecond),
		r.Mean.Round(time.Microsecond), r.P99.Round(time.Microsecond))
}

// Run hammers url with  concurrent clients until ctx is done.
func Run(ctx context.Context, url string, workers int, duration time.Duration) Result {
	if workers < 1 {
		workers = 1
	}

	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			// Without room for the connections these workers open, they queue
			// on the transport and you profile your own load generator.
			MaxIdleConnsPerHost: workers,
		},
	}

	var (
		requests atomic.Int64
		failures atomic.Int64
		mu       sync.Mutex
		latency  []time.Duration
		wg       sync.WaitGroup
	)

	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			local := make([]time.Duration, 0, 1024)

			for ctx.Err() == nil {
				requestStart := time.Now()

				if err := fetch(ctx, client, url); err != nil {
					if ctx.Err() == nil {
						failures.Add(1)
					}

					continue
				}

				requests.Add(1)

				local = append(local, time.Since(requestStart))
			}

			mu.Lock()
			latency = append(latency, local...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	return summarise(requests.Load(), failures.Load(), time.Since(start), latency)
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

	// Drain the body: an unread body keeps the connection from being reused,
	// and then every request pays for a new TCP handshake.
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", response.Status)
	}

	return nil
}

func summarise(requests, failures int64, elapsed time.Duration, latency []time.Duration) Result {
	result := Result{Requests: requests, Errors: failures, Duration: elapsed}

	if len(latency) == 0 {
		return result
	}

	var total time.Duration

	for _, sample := range latency {
		total += sample
	}

	result.Mean = total / time.Duration(len(latency))

	// A partial sort would do, but the sample count here is small and clarity
	// is worth more than the microseconds.
	sorted := make([]time.Duration, len(latency))
	copy(sorted, latency)

	insertionSortDurations(sorted)

	index := int(float64(len(sorted)) * 0.99)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}

	result.P99 = sorted[index]

	return result
}

func insertionSortDurations(values []time.Duration) {
	// values comes from a load run; sort.Slice would allocate a closure per
	// call, and this keeps the load generator out of its own profile.
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
