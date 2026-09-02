// Command api serves the synchronous HTTP surface.
//
// It never blocks on the async work: it commits the order and its event and
// returns 202. The worker binary picks the event up from the shared database.
//
//	go run ./Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/cmd/api -db /tmp/day85.db -addr :8085
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/api"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr = flag.String("addr", ":8085", "listen address")
		dsn  = flag.String("db", "file:day85.db", "sqlite dsn")
	)

	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	db, err := store.Open(*dsn)
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("close db", slog.String("error", err.Error()))
		}
	}()

	service := api.New(store.New(db), cache.NewMemory(), logger)

	server := &http.Server{
		Addr:              *addr,
		Handler:           service.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		logger.Info("api listening", slog.String("addr", *addr), slog.String("db", *dsn))

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
