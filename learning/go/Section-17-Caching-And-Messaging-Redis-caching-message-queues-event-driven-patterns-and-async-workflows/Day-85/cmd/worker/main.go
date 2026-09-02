// Command worker is the async half: a separate binary, scaled separately,
// deployed separately, and restartable without touching the API.
//
// It relays events out of the outbox table and processes them idempotently,
// so a restart mid-batch redelivers rather than loses.
//
//	go run ./Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/cmd/worker -db /tmp/day85.db
//
// -duplicate delivers every event twice, which is how you check in a staging
// environment that the consumer really is idempotent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/queue"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dsn       = flag.String("db", "file:day85.db", "sqlite dsn")
		interval  = flag.Duration("interval", 200*time.Millisecond, "outbox poll interval")
		duplicate = flag.Bool("duplicate", false, "deliver every event twice to exercise idempotency")
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

	dataStore := store.New(db)

	bus := queue.NewBus(logger)
	bus.DeliverTwice(*duplicate)

	receipts := worker.New(dataStore, logger)
	receipts.Register(bus)

	relay := queue.NewRelay(dataStore, bus, *interval, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("worker started",
		slog.String("db", *dsn),
		slog.Duration("interval", *interval),
		slog.Bool("duplicate_delivery", *duplicate))

	relay.Run(ctx)

	processed, duplicates := receipts.Stats()

	logger.Info("worker stopped",
		slog.Int64("relayed", relay.Published()),
		slog.Int64("processed", processed),
		slog.Int64("duplicates_ignored", duplicates))

	return nil
}
