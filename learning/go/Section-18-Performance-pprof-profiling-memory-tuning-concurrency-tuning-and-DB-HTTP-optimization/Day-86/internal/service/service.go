// Package service is the thing under the profiler: an HTTP endpoint that
// renders invoice reports.
//
// GET /report?items=500&mode=slow|fast
//
// One handler, two implementations, so the same load generator can profile
// before and after without changing anything else.
package service

import (
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/report"
)

type Service struct {
	requests atomic.Int64
	nanos    atomic.Int64
}

func New() *Service {
	return &Service{}
}

func (s *Service) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /report", s.renderReport)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("ok\n")); err != nil {
			_ = err
		}
	})

	return mux
}

// Stats returns the request count and the mean latency.
//
// This is the number that has to move. A profile tells you where the time
// goes; only a latency measurement tells you whether removing it mattered.
func (s *Service) Stats() (requests int64, mean time.Duration) {
	count := s.requests.Load()

	if count == 0 {
		return 0, 0
	}

	return count, time.Duration(s.nanos.Load() / count)
}

func (s *Service) Reset() {
	s.requests.Store(0)
	s.nanos.Store(0)
}

func (s *Service) renderReport(w http.ResponseWriter, r *http.Request) {
	itemCount := 500

	if raw := r.URL.Query().Get("items"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 100_000 {
			http.Error(w, "items must be between 0 and 100000", http.StatusBadRequest)

			return
		}

		itemCount = parsed
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "slow"
	}

	// A fixed seed keeps the workload identical across runs, so two profiles
	// are actually comparable.
	items := report.Sample(itemCount, 42)

	start := time.Now()

	var rendered string

	switch mode {
	case "slow":
		rendered = report.RenderSlow(items)

	case "fast":
		rendered = report.RenderFast(items)

	default:
		http.Error(w, "mode must be slow or fast", http.StatusBadRequest)

		return
	}

	elapsed := time.Since(start)

	s.requests.Add(1)
	s.nanos.Add(elapsed.Nanoseconds())

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Render-Mode", mode)
	w.Header().Set("X-Render-Micros", strconv.FormatInt(elapsed.Microseconds(), 10))

	if _, err := w.Write([]byte(rendered)); err != nil {
		_ = err
	}
}

func (s *Service) stats(w http.ResponseWriter, _ *http.Request) {
	requests, mean := s.Stats()

	if _, err := fmt.Fprintf(w, "requests=%d mean=%s\n", requests, mean); err != nil {
		_ = err
	}
}
