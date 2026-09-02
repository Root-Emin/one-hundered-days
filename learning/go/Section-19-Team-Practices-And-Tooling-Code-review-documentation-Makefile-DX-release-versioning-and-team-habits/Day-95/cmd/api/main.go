// Command api serves the task tracker.
//
//	make run
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/cmd/api -addr :8095
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/internal/tasks"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", envOr("TASKS_ADDR", ":8095"), "listen address")

	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	store := tasks.New()

	server := &http.Server{
		Addr:              *addr,
		Handler:           routes(store, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		logger.Info("listening", slog.String("addr", *addr))

		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}

		return nil

	case <-ctx.Done():
		logger.Info("shutting down", slog.Any("counts", store.Counts()))

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	}
}

// routes builds the mux. It is a function rather than inline in run so a test
// can exercise the handlers without starting a server.
func routes(store *tasks.Store, logger *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.List(tasks.Status(r.URL.Query().Get("status"))))
	})

	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title    string `json:"title"`
			Assignee string `json:"assignee"`
		}

		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid body")

			return
		}

		task, err := store.Create(body.Title, body.Assignee)
		if err != nil {
			writeStoreError(w, logger, err)

			return
		}

		writeJSON(w, http.StatusCreated, task)
	})

	mux.HandleFunc("POST /tasks/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid id")

			return
		}

		var body struct {
			Status string `json:"status"`
		}

		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid body")

			return
		}

		task, err := store.Advance(id, tasks.Status(body.Status))
		if err != nil {
			writeStoreError(w, logger, err)

			return
		}

		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

// writeStoreError maps domain errors onto status codes in one place, so the
// same failure cannot be a 400 on one endpoint and a 500 on another.
func writeStoreError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())

	case errors.Is(err, tasks.ErrInvalidTransition):
		// 409: the request was well formed, the state said no.
		writeError(w, http.StatusConflict, "conflict", err.Error())

	case errors.Is(err, tasks.ErrInvalidTask):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())

	default:
		logger.Error("unhandled error", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
