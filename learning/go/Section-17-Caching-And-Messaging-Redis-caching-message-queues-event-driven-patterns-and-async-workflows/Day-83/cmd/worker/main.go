// Command worker consumes order events.
//
// It is a separate binary because it scales on a different signal: queue
// depth, not request rate. Several copies with the same durable name share
// the stream between them.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/events"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	url := envOr("NATS_URL", "nats://127.0.0.1:4222")

	connection, js, err := events.Connect(url)
	if err != nil {
		logger.Error("connect", slog.String("error", err.Error()))
		os.Exit(1)
	}

	defer connection.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := events.EnsureStream(ctx, js); err != nil {
		logger.Error("stream", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if _, err := events.EnsureDeadLetterStream(ctx, js); err != nil {
		logger.Error("dead letter stream", slog.String("error", err.Error()))
		os.Exit(1)
	}

	config := worker.DefaultConfig(envOr("DURABLE", "order-processor"))

	orders := worker.New(js, config, logger, func(ctx context.Context, event events.Event) error {
		order, err := events.Decode[events.OrderCreated](event)
		if err != nil {
			return fmt.Errorf("%w: %w", worker.ErrPoison, err)
		}

		logger.Info("processing order",
			slog.Int64("order_id", order.OrderID),
			slog.String("customer", order.Customer),
			slog.Int64("amount_cents", order.AmountCent))

		// Stand-in for the real work: charge, email, index.
		time.Sleep(100 * time.Millisecond)

		return nil
	})

	stop, err := orders.Start(ctx)
	if err != nil {
		logger.Error("start", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("worker started", slog.String("durable", config.Durable), slog.String("url", url))

	// Report lag on a ticker: this is what a metrics exporter would scrape.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				lag, err := orders.Lag(ctx)
				if err != nil {
					continue
				}

				processed, retried, dead := orders.Counts()

				logger.Info("lag",
					slog.Uint64("pending", lag.Pending),
					slog.Int("ack_pending", lag.AckPending),
					slog.Int("redelivered", lag.Redelivered),
					slog.Int64("processed", processed),
					slog.Int64("retried", retried),
					slog.Int64("dead", dead))
			}
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	<-shutdown

	logger.Info("draining")

	// Stop pulling new messages and let the in-flight ones finish. Unacked
	// messages are redelivered to another worker, so nothing is lost.
	stop()

	time.Sleep(time.Second)

	processed, retried, dead := orders.Counts()

	logger.Info("stopped",
		slog.Int64("processed", processed),
		slog.Int64("retried", retried),
		slog.Int64("dead", dead))
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
