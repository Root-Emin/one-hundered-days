// Command api runs the todo service.
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

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/httpapi"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day70: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	tokens := todo.NewTokenStore()
	tokens.Add(envOr("ADA_TOKEN", "ada-token"), "ada")
	tokens.Add(envOr("ALAN_TOKEN", "alan-token"), "alan")

	service := todo.NewService(tokens, todo.SystemClock{})

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           httpapi.New(service).Routes(),
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

	return server.Shutdown(ctx)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
