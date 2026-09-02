// Command linkr is the capstone service.
//
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-96/cmd/linkr
//	go run ./Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-96/cmd/linkr -addr :9000 -env production
//
// Day 96 is the walking skeleton: it starts, answers its probes, and stops
// cleanly. The links arrive on Day 97.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-96/internal/config"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-96/internal/httpserver"
)

func main() {
	// main does three things and delegates the rest: it is the only place
	// allowed to read the environment, own the signal handler, and exit.
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run is main's testable half: everything it touches arrives as an argument.
func run(args []string, getenv func(string) string, output *os.File) error {
	cfg, err := config.Load(args, getenv)
	if err != nil {
		return err
	}

	logger := newLogger(cfg, output)

	logger.Info("starting", slog.String("config", cfg.String()))

	// NotifyContext gives a cancelled context on SIGINT or SIGTERM, which is
	// what every layer below already knows how to react to. A signal handler
	// that sets a flag needs everything to poll it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := httpserver.New(cfg, logger)

	// Day 97 registers the store here. Until then the skeleton reports ready
	// as soon as it is listening, and says so rather than pretending to check
	// something.
	server.AddChecker("self", httpserver.CheckerFunc(func(context.Context) error { return nil }))

	// Ready is marked after the (currently empty) startup work, in the order
	// production will need: migrate, warm, then accept traffic.
	go func() {
		// A tiny delay so the "starting -> ready" transition is observable in
		// the logs and in a test, rather than happening before the listener.
		time.Sleep(10 * time.Millisecond)

		server.MarkReady()
	}()

	return server.Start(ctx)
}

// newLogger builds the logger the environment calls for: JSON in production
// for the log aggregator, text in development for the human reading it.
func newLogger(cfg config.Config, output *os.File) *slog.Logger {
	level := slog.LevelInfo

	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	options := &slog.HandlerOptions{Level: level}

	if cfg.Production() {
		return slog.New(slog.NewJSONHandler(output, options))
	}

	return slog.New(slog.NewTextHandler(output, options))
}
