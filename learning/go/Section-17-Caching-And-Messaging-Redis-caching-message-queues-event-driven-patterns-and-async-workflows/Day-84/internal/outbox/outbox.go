// Package outbox implements the transactional outbox pattern.
//
// The problem it solves is the dual-write problem:
//
//	tx.Commit()                  // the order is saved
//	broker.Publish(orderCreated) // ...and this fails
//
// Now the order exists and nobody was told. Swap the order and it is worse:
// the event is published for an order that was never committed.
//
// There is no way to make a database commit and a broker publish atomic. The
// outbox sidesteps it: the event is written INTO THE SAME TRANSACTION as the
// business change, into an outbox table. A separate relay reads that table and
// publishes. If the relay crashes, the row is still there. If it publishes
// twice, the consumer deduplicates (which it must anyway).
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

const Schema = `
CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	customer    TEXT    NOT NULL,
	amount_cent INTEGER NOT NULL,
	status      TEXT    NOT NULL DEFAULT 'created',
	created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS outbox (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id      TEXT    NOT NULL,
	aggregate_id  TEXT    NOT NULL,
	event_type    TEXT    NOT NULL,
	payload       TEXT    NOT NULL,
	created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
	published_at  TEXT,
	attempts      INTEGER NOT NULL DEFAULT 0,
	last_error    TEXT
);

-- The relay's query: unpublished rows, oldest first. Without this index it is
-- a full table scan on every poll, forever.
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
	ON outbox (published_at, id) WHERE published_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_event_id ON outbox (event_id);`

type Event struct {
	ID          int64
	EventID     string
	AggregateID string
	Type        string
	Payload     json.RawMessage
	Attempts    int
	CreatedAt   time.Time
}

type Order struct {
	ID         int64
	Customer   string
	AmountCent int64
	Status     string
}

// Store writes business data and events in one transaction.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateOrder is the pattern in six lines: begin, write the row, write the
// event, commit. Either both land or neither does.
func (s *Store) CreateOrder(ctx context.Context, customer string, amountCent int64) (Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, fmt.Errorf("begin: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			// Nothing useful to do; the caller already has the real error.
			_ = err
		}
	}()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO orders (customer, amount_cent) VALUES (?, ?);`, customer, amountCent)
	if err != nil {
		return Order{}, fmt.Errorf("insert order: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Order{}, fmt.Errorf("read order id: %w", err)
	}

	order := Order{ID: id, Customer: customer, AmountCent: amountCent, Status: "created"}

	payload, err := json.Marshal(map[string]any{
		"order_id":     order.ID,
		"customer":     order.Customer,
		"amount_cents": order.AmountCent,
	})
	if err != nil {
		return Order{}, fmt.Errorf("encode event: %w", err)
	}

	// The event id is derived from the aggregate and the event type, so a
	// retry of this whole operation cannot produce two events for one order.
	eventID := fmt.Sprintf("order-%d-created", order.ID)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (event_id, aggregate_id, event_type, payload) VALUES (?, ?, ?, ?);`,
		eventID, fmt.Sprint(order.ID), "order.created", string(payload)); err != nil {
		return Order{}, fmt.Errorf("insert outbox row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Order{}, fmt.Errorf("commit: %w", err)
	}

	return order, nil
}

// Unpublished returns events waiting to be relayed, oldest first.
//
// Order matters: publishing out of order means a consumer can see
// "order.cancelled" before "order.created".
func (s *Store) Unpublished(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, aggregate_id, event_type, payload, attempts, created_at
		 FROM outbox
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("select unpublished: %w", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	var events []Event

	for rows.Next() {
		var (
			event     Event
			payload   string
			createdAt string
		)

		if err := rows.Scan(&event.ID, &event.EventID, &event.AggregateID,
			&event.Type, &payload, &event.Attempts, &createdAt); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}

		event.Payload = json.RawMessage(payload)

		if event.CreatedAt, err = time.Parse(time.DateTime, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox rows: %w", err)
	}

	return events, nil
}

// MarkPublished records success.
//
// Note the ordering in the relay: publish FIRST, then mark. A crash between
// the two republishes the event - at-least-once, which the consumer handles.
// Marking first would risk losing the event entirely, which nothing handles.
func (s *Store) MarkPublished(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET published_at = datetime('now') WHERE id = ?;`, id); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}

	return nil
}

func (s *Store) RecordFailure(ctx context.Context, id int64, reason string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?;`,
		reason, id); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}

	return nil
}

func (s *Store) PendingCount(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox WHERE published_at IS NULL;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending: %w", err)
	}

	return count, nil
}

//
// RELAY
//

// Publisher is whatever actually sends the event: NATS, Kafka, an HTTP
// webhook. The outbox does not care.
type Publisher interface {
	Publish(ctx context.Context, eventType string, event Event) error
}

// Relay drains the outbox.
//
// In production this is either a background goroutine like this one, or
// change-data-capture (Debezium) reading the database's write-ahead log -
// which avoids polling entirely at the cost of more infrastructure.
type Relay struct {
	store     *Store
	publisher Publisher
	logger    *slog.Logger

	interval  time.Duration
	batchSize int

	published atomic.Int64
	failed    atomic.Int64
}

func NewRelay(store *Store, publisher Publisher, interval time.Duration, logger *slog.Logger) *Relay {
	if logger == nil {
		logger = slog.Default()
	}

	if interval <= 0 {
		interval = 200 * time.Millisecond
	}

	return &Relay{store: store, publisher: publisher, logger: logger, interval: interval, batchSize: 50}
}

func (r *Relay) Counts() (published, failed int64) {
	return r.published.Load(), r.failed.Load()
}

// Run polls until the context is cancelled.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := r.drain(ctx); err != nil {
				r.logger.Error("outbox drain failed", slog.String("error", err.Error()))
			}
		}
	}
}

// Drain publishes everything currently pending. Exported so a test - or a
// shutdown path - can flush without waiting for a tick.
func (r *Relay) Drain(ctx context.Context) error {
	return r.drain(ctx)
}

func (r *Relay) drain(ctx context.Context) error {
	events, err := r.store.Unpublished(ctx, r.batchSize)
	if err != nil {
		return err
	}

	for _, event := range events {
		if err := r.publisher.Publish(ctx, event.Type, event); err != nil {
			r.failed.Add(1)

			if recordErr := r.store.RecordFailure(ctx, event.ID, err.Error()); recordErr != nil {
				r.logger.Error("record failure", slog.String("error", recordErr.Error()))
			}

			// Stop at the first failure: continuing would publish later
			// events before this one, and order matters.
			return nil
		}

		if err := r.store.MarkPublished(ctx, event.ID); err != nil {
			// The event WAS published; failing to mark it means it will be
			// published again. That is why consumers deduplicate.
			r.logger.Error("mark published failed",
				slog.String("event_id", event.EventID), slog.String("error", err.Error()))

			return err
		}

		r.published.Add(1)
	}

	return nil
}
