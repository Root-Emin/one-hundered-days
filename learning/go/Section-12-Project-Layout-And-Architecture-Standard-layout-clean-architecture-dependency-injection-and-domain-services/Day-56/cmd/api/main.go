// Command api is the task service binary.
//
// main is deliberately thin: it reads configuration, constructs the concrete
// implementations, wires them together and runs the server. There is not one
// business rule in this file - if a reader wants to know what the service
// *does*, they read internal/service and internal/domain.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/config"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/httpapi"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/repository"
	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-56/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("day56: ")

	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

// run exists so every exit path can return an error and still run its
// defers - log.Fatal in the middle of main skips them.
func run() error {
	settings, err := config.Load()
	if err != nil {
		return err
	}

	// The composition root: the only place where concrete types meet.
	tasks := repository.NewMemoryTaskRepository()
	taskService := service.NewTaskService(tasks, service.SystemClock{})
	handler := httpapi.NewHandler(taskService)

	server := &http.Server{
		Addr:              ":" + settings.Port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * settings.ReadTimeout / 10,
		ReadTimeout:       settings.ReadTimeout,
		WriteTimeout:      settings.WriteTimeout,
		IdleTimeout:       settings.IdleTimeout,
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

	ctx, cancel := context.WithTimeout(context.Background(), settings.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close: %v", closeErr)
		}

		return err
	}

	log.Printf("stopped cleanly")

	return nil
}
