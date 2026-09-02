// Command api runs the reading-list service.
//
// Section 12 capstone: the same MVP as earlier sections, reorganised into
// domain / service / storage / transport packages with the dependency arrow
// pointing inward. See README.md for the package diagram and the rules.
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

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/service"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/storage/sqlite"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-60/internal/transport/httpapi"
)

/*
Run:

	go run ./cmd/api
	DB_PATH=data/library.db go run ./cmd/api
	go run ./cmd/archdiagram      # print the real package diagram from source

Try it:

	curl -XPOST localhost:8080/books -d '{"isbn":"9780134190440","title":"The Go Programming Language","author":"Donovan","pages":380}'
	curl -XPOST localhost:8080/books/1/start
	curl -XPOST localhost:8080/books/1/progress -d '{"page":120}'
	curl localhost:8080/books?status=reading
	curl localhost:8080/stats

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day60: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

// run is the composition root. Reading it top to bottom gives the whole
// dependency graph of the binary.
func run() error {
	ctx := context.Background()

	db, err := sqlite.Open(ctx, envOr("DB_PATH", ":memory:"))
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	repository := sqlite.New(db)
	library := service.NewLibrary(repository, repository, service.SystemClock{})
	handler := httpapi.New(library)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           handler.Routes(),
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

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Printf("stopped cleanly")

	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
