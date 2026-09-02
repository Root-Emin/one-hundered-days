// Command api is the deployable MVP.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/buildinfo"
	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/httpapi"
	"example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/internal/notes"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print build metadata and exit")
		healthcheck = flag.Bool("healthcheck", false, "probe the local server; exit 0 when healthy")
	)

	flag.Parse()

	if *showVersion {
		info := buildinfo.Current()

		fmt.Printf("version   %s\ncommit    %s\nbuilt     %s\ngo        %s\nplatform  %s\n",
			info.Version, info.Commit, info.BuildTime, info.GoVersion, info.Platform)

		return
	}

	if *healthcheck {
		os.Exit(probe())
	}

	logger := newLogger()
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo

	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}

	options := &slog.HandlerOptions{Level: level}

	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)

	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "text") {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	info := buildinfo.Current()

	return slog.New(handler).With(
		slog.String("service", "day80-api"),
		slog.String("version", info.Version),
		slog.String("commit", info.Commit),
	)
}

func run(logger *slog.Logger) error {
	handler := httpapi.New(logger, notes.NewService())

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErrors := make(chan error, 1)

	go func() {
		logger.Info("starting", slog.String("addr", server.Addr), slog.Int("pid", os.Getpid()))

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
		logger.Info("shutdown signal", slog.String("signal", received.String()))
	}

	// Drain: fail readiness first, so the load balancer stops sending new
	// requests while the in-flight ones finish.
	handler.Drain()

	drainDelay := envDuration("DRAIN_DELAY", 2*time.Second)

	logger.Info("draining", slog.Duration("delay", drainDelay))
	time.Sleep(drainDelay)

	ctx, cancel := context.WithTimeout(context.Background(), envDuration("SHUTDOWN_TIMEOUT", 15*time.Second))
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

// probe is the container health check: distroless has no shell and no curl.
func probe() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://127.0.0.1:"+envOr("PORT", "8080")+"/healthz", nil)
	if err != nil {
		return 1
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)

		return 1
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "healthcheck: close:", err)
		}
	}()

	if response.StatusCode != http.StatusOK {
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

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))

	if value == "" {
		return fallback
	}

	if parsed, err := time.ParseDuration(value); err == nil && parsed >= 0 {
		return parsed
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	return fallback
}
