// Command billing wires the layers together and runs the HTTP server.
//
// Read top to bottom, this file is the dependency graph of the whole service:
// adapters are constructed first, injected into the use cases, and the use
// cases are injected into the transport.
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

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/adapter/rest"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/adapter/storage"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/usecase"
)

/*
Day 57 - Clean Architecture Layers

	internal/domain     entities, value objects, ports        (imports: stdlib)
	internal/usecase    application use cases                 (imports: domain)
	internal/adapter/*  storage, events, clock, HTTP          (imports: domain, usecase)
	cmd/billing         wiring                                (imports: all)

The dependency rule is checked mechanically by internal/arch/arch_test.go, so
a violation fails `go test ./...` rather than surviving until someone notices.

Run:

	go run ./cmd/billing

	curl -XPOST localhost:8080/subscriptions -d '{"customer_id":"cus_1","plan":"pro","trial":true}'
	curl localhost:8080/subscriptions/1
	curl -XPUT localhost:8080/subscriptions/1/plan -d '{"plan":"scale"}'
	curl -XPOST localhost:8080/subscriptions/1/activate
	curl localhost:8080/revenue
	curl -XDELETE localhost:8080/subscriptions/1

Test:

	go test ./...
*/

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day57: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	// Outer layer first: the concrete adapters.
	repository := storage.NewMemorySubscriptions()
	events := storage.NewLogPublisher(log.Printf)
	clock := storage.SystemClock{}

	// Inner layer: the use cases, which only ever see the ports.
	subscriptions := usecase.NewSubscriptions(repository, events, clock)

	// Outer layer again: the transport that drives them.
	handler := rest.NewHandler(subscriptions)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
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
