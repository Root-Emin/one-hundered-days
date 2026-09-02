// Package api is the MVP's hot endpoint: GET /dashboard.
//
// One handler, four implementations selected by ?v=1..4, so the same load test
// can measure every round of the optimization cycle against the same traffic.
// In a real project these are four commits; keeping them side by side is what
// makes the before/after table possible.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-90/internal/store"
)

// Version selects an implementation of the hot path.
type Version int

const (
	// V1 is the baseline: N+1 queries against an unindexed column.
	V1 Version = 1
	// V2 adds the index. No application change at all.
	V2 Version = 2
	// V3 replaces N+1 with a single JOIN.
	V3 Version = 3
	// V4 preallocates the result slices, so the page is not grown by
	// repeated append.
	V4 Version = 4
)

func (v Version) String() string {
	switch v {
	case V1:
		return "v1 N+1, no index"
	case V2:
		return "v2 N+1, indexed"
	case V3:
		return "v3 single JOIN"
	case V4:
		return "v4 JOIN + preallocated"
	default:
		return "unknown"
	}
}

type API struct {
	store  *store.Store
	logger *slog.Logger

	mu      sync.Mutex
	samples map[Version]*Samples
}

// Samples is the per-version latency record. Measuring inside the handler
// separates "the query is slow" from "the whole request is slow", which is the
// difference between fixing the database and fixing the framework.
type Samples struct {
	Requests  int
	Queries   int
	Latencies []time.Duration
}

func New(dataStore *store.Store, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}

	return &API{
		store:   dataStore,
		logger:  logger,
		samples: make(map[Version]*Samples),
	}
}

func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /dashboard", a.dashboard)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("ok\n")); err != nil {
			_ = err
		}
	})

	return mux
}

// Stats returns the recorded samples for one version.
func (a *API) Stats(version Version) Samples {
	a.mu.Lock()
	defer a.mu.Unlock()

	samples, found := a.samples[version]
	if !found {
		return Samples{}
	}

	copied := *samples
	copied.Latencies = append([]time.Duration(nil), samples.Latencies...)

	return copied
}

func (a *API) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.samples = make(map[Version]*Samples)
}

func (a *API) record(version Version, queries int, latency time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	samples, found := a.samples[version]
	if !found {
		samples = &Samples{}
		a.samples[version] = samples
	}

	samples.Requests++
	samples.Queries += queries
	samples.Latencies = append(samples.Latencies, latency)
}

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	version := V1

	if raw := r.URL.Query().Get("v"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < int(V1) || parsed > int(V4) {
			http.Error(w, "v must be 1..4", http.StatusBadRequest)

			return
		}

		version = Version(parsed)
	}

	limit := 50

	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			http.Error(w, "limit must be 1..1000", http.StatusBadRequest)

			return
		}

		limit = parsed
	}

	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "pro"
	}

	start := time.Now()

	rows, queries, err := a.load(r, version, tier, limit)
	if err != nil {
		a.logger.Error("dashboard failed", slog.String("error", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	if err := a.write(w, rows); err != nil {
		// The status line is already sent; all that is left is to say so.
		a.logger.Error("write failed", slog.String("error", err.Error()))

		return
	}

	elapsed := time.Since(start)

	a.record(version, queries.Count, elapsed)

	w.Header().Set("X-Queries", strconv.Itoa(queries.Count))
}

func (a *API) load(r *http.Request, version Version, tier string, limit int) ([]store.Row, store.Queries, error) {
	switch version {
	case V1, V2:
		return a.store.DashboardNPlusOne(r.Context(), tier, limit)

	case V3:
		return a.store.DashboardJoined(r.Context(), tier, limit, false)

	case V4:
		return a.store.DashboardJoined(r.Context(), tier, limit, true)

	default:
		return nil, store.Queries{}, fmt.Errorf("unknown version %d", version)
	}
}

// write streams the response.
//
// An earlier round 4 marshalled into a pooled buffer and wrote it in one go.
// It was measured and REVERTED: json.Marshal already builds the whole document
// in its own internal pool, so copying that into a second pooled buffer added
// a full extra copy of the response - 84 KB/op against 64 KB/op for the plain
// encoder. The pool made it worse.
//
// That is what "optimize with evidence" means in practice. The change sounded
// right, the benchmark disagreed, and the benchmark wins.
func (a *API) write(w http.ResponseWriter, rows []store.Row) error {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(rows); err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	return nil
}
