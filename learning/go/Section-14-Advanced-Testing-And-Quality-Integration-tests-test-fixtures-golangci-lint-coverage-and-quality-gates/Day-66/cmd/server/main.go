// Command server runs the bookmarks API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/api"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-66/internal/store"
)

/*
Day 66 - Advanced Testing & Quality: Integration Testing

Tasks covered:

 1. HTTP tested end to end: real handlers, real store, real database
 2. httptest.Server used so the tests speak actual HTTP over a real socket
 3. Fixtures seeded before each case and cleaned up after, reliably
 4. Slow tests behind a build tag, so `go test ./...` stays fast

Files:

	internal/store             persistence
	internal/api               HTTP layer
	internal/testsupport       shared fixtures and helpers (test-only package)
	internal/api/api_test.go   fast tests, always run
	internal/api/integration_test.go  tagged 'integration'

Run:

	go run ./cmd/server
	curl -XPOST localhost:8080/bookmarks -H 'X-Owner: ada' \
	  -d '{"url":"https://go.dev","title":"Go","tags":["go"]}'
	curl localhost:8080/bookmarks -H 'X-Owner: ada'

Test:

	go test ./...                        # fast tests only
	go test -tags=integration ./...      # + the slow end-to-end suite
	go test -tags=integration -race -count=1 ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day66: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	ctx := context.Background()

	db, err := store.Open(ctx, envOr("DB_PATH", ":memory:"))
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           api.New(store.New(db)).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s", server.Addr)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err
	case received := <-shutdown:
		log.Printf("shutdown signal: %s", received)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
