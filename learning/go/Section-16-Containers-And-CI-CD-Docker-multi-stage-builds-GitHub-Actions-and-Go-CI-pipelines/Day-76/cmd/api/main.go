// Command api is the binary that goes into the container image.
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
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-76/internal/api"
)

/*
Day 76 - Containers & CI/CD: Dockerizing Go Apps

Tasks covered:

 1. A Dockerfile that builds this service on a minimal base
 2. A multi-stage build: compile in a toolchain image, ship only the binary
 3. ENTRYPOINT set so the binary is PID 1 and receives SIGTERM directly
 4. Build and run locally, with ports and environment variables

Files:

	Dockerfile        the multi-stage build
	.dockerignore     what must never enter the build context
	docker-compose.yml  local run with ports and env
	main.go           the signal handling that makes PID 1 behave

Build and run:

	# from the repository root, because the Dockerfile needs go.mod
	cd learning/go
	docker build -f Section-16-.../Day-76/Dockerfile -t day76-api:dev .
	docker run --rm -p 8080:8080 -e PORT=8080 day76-api:dev

	curl localhost:8080/healthz
	curl localhost:8080/version        # shows uid: 65532, not 0
	curl -XPOST localhost:8080/notes -d '{"text":"hello from a container"}'

	# graceful shutdown: SIGTERM must be handled, not ignored
	docker stop $(docker ps -q --filter ancestor=day76-api:dev)

Without Docker:

	go run ./cmd/api
	go run .              # the day's explainer program

Environment variables:

	PORT              Default: 8080
	SHUTDOWN_TIMEOUT  Default: 15s
	VERSION           Injected at build time with -ldflags
*/

// Build metadata, set at link time:
//
//	-ldflags "-X main.version=v1.2.3 -X main.commit=abc123 -X main.buildTime=..."
//
// Baking them in beats reading a file at startup: the binary always knows
// what it is, even when it is copied out of the image.
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	// A distroless image has no shell and no curl, so the container's health
	// check has to be the binary itself. This flag is that probe.
	healthcheck := flag.Bool("healthcheck", false, "probe the local server and exit 0 if healthy")

	flag.Parse()

	if *healthcheck {
		os.Exit(probe())
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	service := api.New(logger, version)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           service.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	go func() {
		logger.Info("starting",
			slog.String("addr", server.Addr),
			slog.String("version", version),
			slog.String("commit", commit),
			slog.String("build_time", buildTime),
			slog.Int("uid", os.Getuid()),
			slog.Int("pid", os.Getpid()))

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// The signal half of "ENTRYPOINT, not CMD with a shell".
	//
	// docker stop sends SIGTERM to PID 1 and waits (10s by default) before
	// SIGKILL. If the binary is PID 1 it gets the signal directly. If it was
	// started through a shell ("CMD ./app"), the SHELL is PID 1, it does not
	// forward signals, and every deploy kills connections mid-request.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return err

	case received := <-shutdown:
		logger.Info("shutdown signal received", slog.String("signal", received.String()))
	}

	// Step 1: fail readiness. The load balancer notices within a poll or two
	// and stops sending new requests, while the ones in flight finish.
	service.NotReady()

	timeout := 15 * time.Second

	if value := os.Getenv("SHUTDOWN_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			timeout = parsed
		}
	}

	// Step 2: stop accepting, drain what is in flight. The budget must be
	// shorter than the orchestrator's grace period, or SIGKILL arrives first
	// and the drain was pointless.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("force close", slog.String("error", closeErr.Error()))
		}

		return err
	}

	logger.Info("stopped cleanly")

	return nil
}

// probe is the health check the container image runs against itself.
func probe() int {
	address := "http://127.0.0.1:" + envOr("PORT", "8080") + "/healthz"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)

		return 1
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)

		return 1
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck: close body:", err)
		}
	}()

	if response.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", response.StatusCode)

		return 1
	}

	return 0
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
