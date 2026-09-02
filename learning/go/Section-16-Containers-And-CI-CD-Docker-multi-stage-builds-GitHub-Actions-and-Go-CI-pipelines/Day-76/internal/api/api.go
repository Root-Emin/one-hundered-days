// Package api is a small service, kept deliberately simple: the subject of
// this section is how it ships, not what it does.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"
)

type API struct {
	logger  *slog.Logger
	version string
	started time.Time

	mu    sync.RWMutex
	notes map[int64]string
	next  int64

	// ready flips to true once startup work is done, and back to false when a
	// shutdown begins. A container orchestrator polls it to decide whether to
	// send traffic.
	ready atomic
}

// atomic is a tiny mutex-protected bool; sync/atomic.Bool would do, and is
// used here through a named type so the intent reads clearly at the call site.
type atomic struct {
	mu    sync.RWMutex
	value bool
}

func (a *atomic) Set(value bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.value = value
}

func (a *atomic) Get() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.value
}

func New(logger *slog.Logger, version string) *API {
	if logger == nil {
		logger = slog.Default()
	}

	api := &API{
		logger:  logger,
		version: version,
		started: time.Now(),
		notes:   make(map[int64]string),
		next:    1,
	}

	api.ready.Set(true)

	return api
}

// NotReady is called at the start of a graceful shutdown, so the load
// balancer stops sending new requests before the server stops accepting them.
func (a *API) NotReady() { a.ready.Set(false) }

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.readiness)
	mux.HandleFunc("GET /version", a.versionInfo)
	mux.HandleFunc("POST /notes", a.createNote)
	mux.HandleFunc("GET /notes", a.listNotes)

	return a.logging(mux)
}

func (a *API) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		a.logger.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Duration("duration", time.Since(start).Round(time.Microsecond)))
	})
}

// health is liveness: it must not depend on anything external, or a
// dependency outage makes the orchestrator restart every container.
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"uptime": time.Since(a.started).Round(time.Second).String(),
	})
}

// readiness is what the load balancer polls.
func (a *API) readiness(w http.ResponseWriter, r *http.Request) {
	if !a.ready.Get() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting down"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// versionInfo is what makes "which build is running?" answerable in
// production. The values are injected at build time with -ldflags.
func (a *API) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    a.version,
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"hostname":   hostname(),
		"uid":        os.Getuid(),
	})
}

func (a *API) createNote(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if input.Text == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "text is required"})
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	id := a.next
	a.notes[id] = input.Text
	a.next++

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "text": input.Text})
}

func (a *API) listNotes(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	notes := make([]map[string]any, 0, len(a.notes))

	for id, text := range a.notes {
		notes = append(notes, map[string]any{"id": id, "text": text})
	}

	writeJSON(w, http.StatusOK, map[string]any{"notes": notes, "count": len(notes)})
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	return name
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", slog.String("error", err.Error()))
	}
}
