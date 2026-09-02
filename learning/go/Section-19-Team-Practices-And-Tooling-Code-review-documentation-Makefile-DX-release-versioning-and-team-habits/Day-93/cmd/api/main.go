// Command api serves the notes service.
//
//	make run                       # with the defaults from .envrc
//	go run ./Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/cmd/api -addr :8093
//
// It applies pending migrations on startup. That is a deliberate choice for a
// single-instance service: a newcomer running "make run" on a fresh clone gets
// a working service, not a foreign key error. With several replicas it belongs
// in a job instead, because two instances migrating at once is a race.
package main

import (
	"context"
	"database/sql"
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

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/assets"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/migrate"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93/internal/notes"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr = flag.String("addr", envOr("NOTES_ADDR", ":8093"), "listen address")
		dsn  = flag.String("db", envOr("NOTES_DB", "notes.db"), "sqlite database file")
	)

	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	db, err := sql.Open("sqlite", "file:"+*dsn+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open %s: %w", *dsn, err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("close db", slog.String("error", err.Error()))
		}
	}()

	migrations, err := migrate.Load(assets.Migrations, assets.MigrationsDir)
	if err != nil {
		return err
	}

	applied, err := migrate.Up(context.Background(), db, migrations)
	if err != nil {
		return err
	}

	logger.Info("migrations up to date",
		slog.Int("applied_now", len(applied)), slog.Int("total", len(migrations)))

	store := notes.New(db)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /notes", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.List(r.Context())
		if err != nil {
			logger.Error("list notes", slog.String("error", err.Error()))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		writeJSON(w, http.StatusOK, list)
	})

	mux.HandleFunc("POST /notes", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}

		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)

			return
		}

		note, err := store.Create(r.Context(), body.Title, body.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		writeJSON(w, http.StatusCreated, note)
	})

	mux.HandleFunc("GET /notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)

			return
		}

		note, err := store.Get(r.Context(), id)
		if errors.Is(err, notes.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}

		if err != nil {
			logger.Error("get note", slog.String("error", err.Error()))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		writeJSON(w, http.StatusOK, note)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		logger.Info("listening", slog.String("addr", *addr), slog.String("db", *dsn))

		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}

		return nil

	case <-ctx.Done():
		logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
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
