// Command server is the profiling target: the application on one port, pprof
// on another.
//
//	go run ./Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/cmd/server -addr :8086 -pprof 127.0.0.1:6060
//
// Then, in another terminal:
//
//	go tool pprof -http=: http://127.0.0.1:6060/debug/pprof/profile?seconds=15
//	go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
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

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/profiling"
	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-86/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr        = flag.String("addr", ":8086", "application listen address")
		pprofAddr   = flag.String("pprof", "127.0.0.1:6060", "pprof listen address (keep it on localhost)")
		blockAndMux = flag.Bool("contention", false, "enable block and mutex profiles (they cost something)")
	)

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *blockAndMux {
		profiling.EnableBlockAndMutexProfiles()
	}

	debug, err := profiling.StartDebugServer(*pprofAddr)
	if err != nil {
		return err
	}

	defer func() {
		if err := debug.Close(); err != nil {
			logger.Error("close pprof server", slog.String("error", err.Error()))
		}
	}()

	logger.Info("pprof listening", slog.String("url", debug.URL()+"/debug/pprof/"))

	app := service.New()

	server := &http.Server{Addr: *addr, Handler: app.Routes(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		logger.Info("application listening", slog.String("addr", *addr))

		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}

		return nil

	case <-ctx.Done():
		requests, mean := app.Stats()

		logger.Info("shutting down",
			slog.Int64("requests", requests), slog.String("mean_latency", mean.String()))

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	}
}
