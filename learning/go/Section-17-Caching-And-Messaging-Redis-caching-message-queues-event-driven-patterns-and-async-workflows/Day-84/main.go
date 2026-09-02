// Day 84 - Event-Driven Patterns.
//
// Four problems, four patterns, one demo:
//
//  1. OUTBOX - the dual-write problem. You cannot atomically write to your
//     database and publish to a broker. Write the event to the database
//     instead, in the same transaction, and relay it afterwards.
//
//  2. IDEMPOTENT HANDLERS - at-least-once delivery means duplicates are
//     normal, not exceptional. Claim the event in the same transaction as the
//     work, and the second delivery is a no-op.
//
//  3. DEAD LETTER QUEUE - a message that can never succeed must leave the hot
//     path, or it blocks everything behind it.
//
//  4. SAGA - a workflow spanning services has no distributed transaction.
//     Give every step an inverse and undo in reverse order.
//
// Run: go run ./Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/bus"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/idempotency"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/outbox"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/saga"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Quiet logger for the narrative; the packages log at Warn and above so
	// failures still show up.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	db, err := open()
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close:", err)
		}
	}()

	if err := demoOutbox(ctx, db, logger); err != nil {
		return err
	}

	if err := demoIdempotency(ctx, db, logger); err != nil {
		return err
	}

	if err := demoDeadLetterQueue(ctx, db, logger); err != nil {
		return err
	}

	demoSaga(ctx, logger)

	return nil
}

func open() (*sql.DB, error) {
	// One shared in-memory database: "cache=shared" plus a name, otherwise
	// every pooled connection gets its own empty database.
	db, err := sql.Open("sqlite", "file:day84?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	// The in-memory database lives as long as a connection to it does.
	db.SetMaxOpenConns(1)

	for _, schema := range []string{outbox.Schema, idempotency.Schema} {
		if _, err := db.Exec(schema); err != nil {
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}

	return db, nil
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, repeat(len(title)))
}

func repeat(n int) string {
	line := make([]byte, n)

	for i := range line {
		line[i] = '='
	}

	return string(line)
}

// ---------------------------------------------------------------------------
// 1. OUTBOX
// ---------------------------------------------------------------------------

func demoOutbox(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	section("1. Outbox: the dual-write problem")

	store := outbox.NewStore(db)
	broker := bus.New(logger, 3)

	received := 0

	broker.Subscribe("order.created", "audit", func(context.Context, bus.Delivery) error {
		received++

		return nil
	})

	relay := outbox.NewRelay(store, broker, 50*time.Millisecond, logger)

	// The broker is down. In the naive design - write row, then publish - the
	// event is lost here, and the order exists that nobody downstream knows
	// about.
	broker.Break("order.created")

	fmt.Println("broker is DOWN; creating two orders anyway")

	for _, customer := range []string{"ada", "grace"} {
		order, err := store.CreateOrder(ctx, customer, 4200)
		if err != nil {
			return err
		}

		fmt.Printf("  created order %d for %s (row + event in ONE transaction)\n", order.ID, order.Customer)
	}

	if err := relay.Drain(ctx); err != nil {
		return err
	}

	pending, err := store.PendingCount(ctx)
	if err != nil {
		return err
	}

	published, failed := relay.Counts()

	fmt.Printf("  relay: published=%d failed=%d, outbox still holds %d event(s)\n", published, failed, pending)
	fmt.Println("  nothing was lost - the events are durable rows, not in-flight packets")

	broker.Heal("order.created")

	fmt.Println("broker is BACK UP")

	if err := relay.Drain(ctx); err != nil {
		return err
	}

	pending, err = store.PendingCount(ctx)
	if err != nil {
		return err
	}

	published, failed = relay.Counts()

	fmt.Printf("  relay: published=%d failed=%d, pending=%d, consumer saw %d event(s)\n",
		published, failed, pending, received)

	return nil
}

// ---------------------------------------------------------------------------
// 2. IDEMPOTENT HANDLERS
// ---------------------------------------------------------------------------

func demoIdempotency(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	section("2. Idempotent handlers: the same event, twice")

	_ = logger

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS emails (id INTEGER PRIMARY KEY AUTOINCREMENT, recipient TEXT NOT NULL);`); err != nil {
		return fmt.Errorf("create emails table: %w", err)
	}

	store := idempotency.NewStore(db)

	send := func(recipient string) idempotency.Handler {
		return func(ctx context.Context, tx *sql.Tx) (string, error) {
			// The side effect and the claim commit together. That is the
			// whole trick: there is no window where one exists without the
			// other.
			if _, err := tx.ExecContext(ctx, `INSERT INTO emails (recipient) VALUES (?);`, recipient); err != nil {
				return "", err
			}

			return "sent", nil
		}
	}

	const eventID = "order-1-created"

	for attempt := 1; attempt <= 3; attempt++ {
		err := store.Process(ctx, "mailer", eventID, send("ada@example.com"))

		switch {
		case err == nil:
			fmt.Printf("  delivery %d: processed, email sent\n", attempt)

		case errors.Is(err, idempotency.ErrAlreadyProcessed):
			// Not an error path: ack it and move on.
			fmt.Printf("  delivery %d: duplicate, skipped\n", attempt)

		default:
			return err
		}
	}

	var emails int

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emails;`).Scan(&emails); err != nil {
		return err
	}

	fmt.Printf("  3 deliveries -> %d email(s) actually sent\n", emails)

	// A different consumer must still get its turn: the claim is scoped to
	// (consumer, event), not to the event alone.
	if err := store.Process(ctx, "analytics", eventID, func(context.Context, *sql.Tx) (string, error) {
		return "counted", nil
	}); err != nil {
		return err
	}

	fmt.Println("  a second consumer (analytics) still processes the same event once")

	return nil
}

// ---------------------------------------------------------------------------
// 3. DEAD LETTER QUEUE
// ---------------------------------------------------------------------------

func demoDeadLetterQueue(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	section("3. Dead letter queue: the message that never succeeds")

	// Silence the expected failure logs; they are the point of the demo, but
	// they would drown the narrative.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	_ = logger

	store := outbox.NewStore(db)
	broker := bus.New(quiet, 3)

	poisoned := true

	broker.Subscribe("order.created", "shipping", func(_ context.Context, delivery bus.Delivery) error {
		var payload struct {
			OrderID int64 `json:"order_id"`
		}

		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			return err
		}

		if poisoned {
			return fmt.Errorf("no shipping rate for order %d", payload.OrderID)
		}

		fmt.Printf("  shipping handled order %d on attempt %d (redelivered=%t)\n",
			payload.OrderID, delivery.Attempt, delivery.Redelivered)

		return nil
	})

	order, err := store.CreateOrder(ctx, "poison", 100)
	if err != nil {
		return err
	}

	relay := outbox.NewRelay(store, broker, 50*time.Millisecond, quiet)

	if err := relay.Drain(ctx); err != nil {
		return err
	}

	dead := broker.DeadLetters()

	fmt.Printf("  order %d failed 3 delivery attempts -> %d message(s) in the DLQ\n", order.ID, len(dead))

	for _, letter := range dead {
		fmt.Printf("    %s: %s\n", letter.Delivery.EventID, letter.Reason)
	}

	fmt.Println("  the queue is NOT blocked: later events keep flowing")

	// An operator fixes the bug and redrives. The DLQ is a queue, not a
	// graveyard.
	poisoned = false

	redriven := broker.Redrive(ctx)

	fmt.Printf("  after the fix, redrove %d message(s); DLQ now holds %d\n", redriven, len(broker.DeadLetters()))

	return nil
}

// ---------------------------------------------------------------------------
// 4. SAGA
// ---------------------------------------------------------------------------

func demoSaga(ctx context.Context, logger *slog.Logger) {
	section("4. Saga: a workflow with no distributed transaction")

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	_ = logger

	build := func(shippingFails bool) *saga.Saga {
		var ledger []string

		note := func(line string) {
			ledger = append(ledger, line)

			fmt.Println("    " + line)
		}

		return saga.New("place-order", quiet,
			saga.Step{
				Name: "reserve-stock",
				Execute: func(_ context.Context, data *saga.Data) error {
					data.Set("reservation", "res-991")
					note("reserved 1 unit")

					return nil
				},
				Compensate: func(_ context.Context, data *saga.Data) error {
					id, _ := data.Get("reservation")
					note(fmt.Sprintf("released reservation %v", id))

					return nil
				},
			},
			saga.Step{
				Name: "charge-card",
				Execute: func(_ context.Context, data *saga.Data) error {
					data.Set("charge", "ch-427")
					note("charged 42.00")

					return nil
				},
				Compensate: func(_ context.Context, data *saga.Data) error {
					id, _ := data.Get("charge")
					// A refund is not an undo - the money moved twice and both
					// movements are on the statement. Compensation restores
					// the balance, not the history.
					note(fmt.Sprintf("refunded charge %v", id))

					return nil
				},
			},
			saga.Step{
				Name: "schedule-shipping",
				Execute: func(context.Context, *saga.Data) error {
					if shippingFails {
						return errors.New("carrier rejected the address")
					}

					note("shipping scheduled")

					return nil
				},
				Compensate: func(context.Context, *saga.Data) error {
					note("cancelled shipment")

					return nil
				},
			},
		)
	}

	happy := build(false)

	fmt.Print(happy.Describe())
	fmt.Println("  happy path:")

	if err := happy.Run(ctx, saga.NewData()); err != nil {
		fmt.Println("  unexpected:", err)
	}

	fmt.Printf("  state=%s\n", happy.State())

	fmt.Println("  failure path (shipping rejects the address):")

	unhappy := build(true)

	err := unhappy.Run(ctx, saga.NewData())

	fmt.Printf("  state=%s compensated=%t\n", unhappy.State(), errors.Is(err, saga.ErrCompensated))
	fmt.Println("  the steps were undone in REVERSE order, so nothing depends on something already gone")
}
