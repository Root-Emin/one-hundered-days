// Package httpapi is the transport layer of the deployable MVP.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/buildinfo"
	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/notes"
)

type Handler struct {
	logger  *slog.Logger
	service *notes.Service

	// ready is flipped false at the start of a graceful shutdown so the load
	// balancer drains this instance before it stops accepting.
	ready atomic.Bool

	requests atomic.Int64
	errors   atomic.Int64
}

func New(logger *slog.Logger, service *notes.Service) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	handler := &Handler{logger: logger, service: service}
	handler.ready.Store(true)

	return handler
}

// Drain is called on SIGTERM, before the server stops accepting.
func (h *Handler) Drain() { h.ready.Store(false) }

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.readiness)
	mux.HandleFunc("GET /version", h.version)
	mux.HandleFunc("GET /metrics", h.metrics)

	mux.HandleFunc("POST /notes", h.create)
	mux.HandleFunc("GET /notes", h.list)
	mux.HandleFunc("GET /notes/{id}", h.get)
	mux.HandleFunc("DELETE /notes/{id}", h.delete)

	return h.observe(mux)
}

func (h *Handler) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		h.requests.Add(1)

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		if recorder.status >= 500 {
			h.errors.Add(1)
		}

		h.logger.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Duration("duration", time.Since(start).Round(time.Microsecond)))
	})
}

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// health is liveness: no dependencies, so a dependency outage does not cause
// a restart loop.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readiness is what the load balancer polls.
func (h *Handler) readiness(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "draining"})

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildinfo.Current())
}

// metrics is a hand-rolled exposition, so this day's image has no extra
// dependency. Section 15 has the Prometheus client version.
func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Current()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fields := []struct {
		name  string
		help  string
		kind  string
		value string
	}{
		{"app_requests_total", "Requests served.", "counter", strconv.FormatInt(h.requests.Load(), 10)},
		{"app_errors_total", "Requests that failed with a 5xx.", "counter", strconv.FormatInt(h.errors.Load(), 10)},
		{"app_notes_stored", "Notes currently stored.", "gauge", strconv.Itoa(h.service.Count())},
	}

	var builder strings.Builder

	for _, field := range fields {
		builder.WriteString("# HELP " + field.name + " " + field.help + "\n")
		builder.WriteString("# TYPE " + field.name + " " + field.kind + "\n")
		builder.WriteString(field.name + " " + field.value + "\n")
	}

	// A build_info gauge with the version as a label is the standard way to
	// answer "which version is running?" from a dashboard.
	builder.WriteString("# HELP app_build_info Build metadata; the value is always 1.\n")
	builder.WriteString("# TYPE app_build_info gauge\n")
	builder.WriteString("app_build_info{version=\"" + info.Version +
		"\",commit=\"" + info.Commit + "\",go=\"" + info.GoVersion + "\"} 1\n")

	if _, err := w.Write([]byte(builder.String())); err != nil {
		h.logger.Error("write metrics", slog.String("error", err.Error()))
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	if !decodeJSON(w, r, &input) {
		return
	}

	note, err := h.service.Create(r.Context(), input.Title, input.Body)
	if err != nil {
		respondError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, note)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	found, err := h.service.List(r.Context(), limit)
	if err != nil {
		respondError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"notes": found, "count": len(found)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	note, err := h.service.ByID(r.Context(), id)
	if err != nil {
		respondError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		respondError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")

		return 0, false
	}

	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer func() {
		if err := r.Body.Close(); err != nil {
			slog.Warn("close body", slog.String("error", err.Error()))
		}
	}()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")

		return false
	}

	return true
}

func respondError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notes.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")

	case errors.Is(err, notes.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "invalid note: "))

	default:
		slog.Error("internal error", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", slog.String("error", err.Error()))
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
