// Package worker is the async half of the MVP: it turns "an order was placed"
// into the slow work that must not sit inside the HTTP request - here, writing
// a receipt.
//
// The handler is built for redelivery. It does not check "have I seen this?"
// and then act (two statements, and a race between them); it claims the event
// in the same transaction as the work, so the database decides the winner.
package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/queue"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
)

const Consumer = "receipts"

type Worker struct {
	store  *store.Store
	logger *slog.Logger

	processed  atomic.Int64
	duplicates atomic.Int64
}

func New(s *store.Store, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{store: s, logger: logger}
}

func (w *Worker) Stats() (processed, duplicates int64) {
	return w.processed.Load(), w.duplicates.Load()
}

// Register wires the worker to the bus. Keeping this here means main only
// says "worker.Register(bus)" instead of knowing the subject names.
func (w *Worker) Register(bus *queue.Bus) {
	bus.Subscribe("order.placed", Consumer, w.HandleOrderPlaced)
}

func (w *Worker) HandleOrderPlaced(ctx context.Context, delivery queue.Delivery) error {
	var order store.Order

	if err := json.Unmarshal(delivery.Payload, &order); err != nil {
		// A payload that cannot be parsed will never parse. Retrying is
		// pointless; in a real system this goes straight to the DLQ.
		return fmt.Errorf("decode %s: %w", delivery.EventID, err)
	}

	err := w.store.ProcessOnce(ctx, Consumer, delivery.EventID, func(ctx context.Context, tx *sql.Tx) error {
		body := fmt.Sprintf("receipt for order %d: %d x product %d = %d cents",
			order.ID, order.Quantity, order.ProductID, order.AmountCent)

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO receipts (order_id, body) VALUES (?, ?);`, order.ID, body); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE orders SET status = 'confirmed' WHERE id = ?;`, order.ID); err != nil {
			return err
		}

		return nil
	})

	switch {
	case err == nil:
		w.processed.Add(1)

		w.logger.Info("order processed",
			slog.String("event_id", delivery.EventID), slog.Int64("order_id", order.ID))

		return nil

	case errors.Is(err, store.ErrAlreadyProcessed):
		// The expected outcome of a redelivery. Ack it and move on: an error
		// here would make the broker redeliver forever.
		w.duplicates.Add(1)

		w.logger.Info("duplicate delivery ignored",
			slog.String("event_id", delivery.EventID), slog.Int("attempt", delivery.Attempt))

		return nil

	default:
		return err
	}
}
