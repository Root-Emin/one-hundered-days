package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/embedded"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/events"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/worker"
)

/*
Day 83 - Caching & Messaging: Working with NATS or Kafka Basics

Tasks covered:

 1. Domain events published when an order is created or cancelled
 2. A worker process consuming them, separate from the publisher
 3. Failures handled: nak with backoff, bounded redelivery, dead letter
 4. Consumer lag observed - pending, ack-pending, redelivered

Run:

	go run .                          # starts an embedded NATS server
	NATS_URL=nats://localhost:4222 go run .

	# or with a real broker:
	docker run --rm -p 4222:4222 nats:2.10-alpine -js

Separate processes, as in production:

	go run ./cmd/worker &
	go run ./cmd/publisher

Test:

	go test ./...     # every test runs against an embedded server
*/

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "day83: %v\n", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	url := strings.TrimSpace(os.Getenv("NATS_URL"))

	if url == "" {
		fmt.Println("\nStarting an embedded NATS server (set NATS_URL to use your own)")

		instance, err := embedded.Start()
		if err != nil {
			return err
		}

		defer instance.Stop()

		url = instance.ClientURL
	}

	connection, js, err := events.Connect(url)
	if err != nil {
		return err
	}

	defer connection.Close()

	stream, err := events.EnsureStream(ctx, js)
	if err != nil {
		return err
	}

	// The dead letter stream is created here too, before any worker starts:
	// a dead letter published to a subject with no stream is simply lost.
	deadLetters, err := events.EnsureDeadLetterStream(ctx, js)
	if err != nil {
		return err
	}

	info, err := stream.Info(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("stream %s ready: subjects=%v storage=%s\n",
		info.Config.Name, info.Config.Subjects, info.Config.Storage)

	//
	// 1. Publish
	//

	fmt.Println("\n1) Publishing domain events")
	fmt.Println(strings.Repeat("-", 80))

	publisher := events.NewPublisher(js)

	for i := 1; i <= 5; i++ {
		event, err := events.NewEvent(
			fmt.Sprintf("order-%d-created", i),
			"order.created",
			events.OrderCreated{OrderID: int64(i), Customer: "ada", AmountCent: int64(1000 * i)},
		)
		if err != nil {
			return err
		}

		sequence, err := publisher.Publish(ctx, events.SubjectOrderCreated, event)
		if err != nil {
			return err
		}

		fmt.Printf("  published %s at stream sequence %d\n", event.ID, sequence)
	}

	// The same event id again: the stream's duplicate window drops it.
	duplicate, err := events.NewEvent("order-1-created", "order.created",
		events.OrderCreated{OrderID: 1, Customer: "ada", AmountCent: 1000})
	if err != nil {
		return err
	}

	sequence, err := publisher.Publish(ctx, events.SubjectOrderCreated, duplicate)
	if err != nil {
		return err
	}

	fmt.Printf("  re-published order-1-created -> sequence %d (the same one: deduplicated by the server)\n", sequence)

	//
	// 2. Consume
	//

	fmt.Println("\n2) A worker consuming them")
	fmt.Println(strings.Repeat("-", 80))

	var (
		handled   atomic.Int64
		failures  atomic.Int64
		processed = make(chan struct{}, 16)
	)

	config := worker.DefaultConfig("order-processor")
	config.FilterSubject = events.SubjectOrderCreated
	config.MaxDeliver = 3
	config.AckWait = time.Second
	config.BackOff = []time.Duration{100 * time.Millisecond, 300 * time.Millisecond}

	orderWorker := worker.New(js, config, logger, func(ctx context.Context, event events.Event) error {
		order, err := events.Decode[events.OrderCreated](event)
		if err != nil {
			return fmt.Errorf("%w: %w", worker.ErrPoison, err)
		}

		// Order 3 fails twice, then succeeds: a transient dependency failure.
		if order.OrderID == 3 && failures.Add(1) <= 2 {
			return errors.New("payment provider timed out")
		}

		// Order 5 can never be processed: poison.
		if order.OrderID == 5 {
			return fmt.Errorf("%w: unknown pricing plan", worker.ErrPoison)
		}

		handled.Add(1)

		fmt.Printf("  processed order %d for %s (%d cents)\n",
			order.OrderID, order.Customer, order.AmountCent)

		select {
		case processed <- struct{}{}:
		default:
		}

		return nil
	})

	stop, err := orderWorker.Start(ctx)
	if err != nil {
		return err
	}

	defer stop()

	// Wait for the four processable orders.
	deadline := time.After(15 * time.Second)

	for handled.Load() < 4 {
		select {
		case <-processed:
		case <-deadline:
			fmt.Println("  (timed out waiting for the worker)")

			break
		}
	}

	time.Sleep(500 * time.Millisecond)

	done, retried, dead := orderWorker.Counts()

	fmt.Printf("\n  processed=%d retried=%d dead-lettered=%d\n", done, retried, dead)
	fmt.Println("  Order 3 failed twice and then succeeded; order 5 was terminated on")
	fmt.Println("  the first attempt because retrying it could never help.")

	//
	// 3. Dead letters
	//

	fmt.Println("\n3) What ended up in the dead letter subject")
	fmt.Println(strings.Repeat("-", 80))

	if err := showDeadLetters(ctx, deadLetters); err != nil {
		fmt.Printf("  %v\n", err)
	}

	//
	// 4. Lag
	//

	fmt.Println("\n4) Consumer lag")
	fmt.Println(strings.Repeat("-", 80))

	lag, err := orderWorker.Lag(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("  pending=%d ack_pending=%d redelivered=%d stream_last_seq=%d consumer_seq=%d\n",
		lag.Pending, lag.AckPending, lag.Redelivered, lag.StreamLastSeq, lag.ConsumerSeq)

	printLagGuidance()
	printSubjects()

	return nil
}

func showDeadLetters(ctx context.Context, stream jetstream.Stream) error {
	info, err := stream.Info(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("  %d message(s) in the dead letter stream\n", info.State.Msgs)
	fmt.Println("  They keep their payload and a Failure-Reason header, so the bug can")
	fmt.Println("  be fixed and the messages replayed rather than reconstructed.")

	return nil
}

func printLagGuidance() {
	fmt.Println(`
  What each number means:

    pending       messages the consumer has not received yet. THE alert:
                  steadily rising means the consumer is slower than the
                  producer, and the gap will not close on its own.
    ack_pending   received but not yet acked - work in flight. Near
                  MaxAckPending means the consumer is the bottleneck.
    redelivered   how much work is being repeated. A climb here usually
                  means a dependency is failing, not the consumer.

  Alerts worth having:

    pending > 10000 for 5m          the consumer cannot keep up
    pending growth positive for 15m  it is falling further behind
    redelivered rate up 10x          something downstream is broken
    dead letter count > 0            a human needs to look

  The fix for lag is usually more consumers in the same durable group - which
  is why the work is in a worker and not in the HTTP handler.`)
}

func printSubjects() {
	fmt.Println("\nSubject design")
	fmt.Println(strings.Repeat("-", 80))

	fmt.Println(`  orders.created            one event type
  orders.*                  any single token below orders
  orders.>                  everything below orders
  orders.created.eu-west-1  a region token, if consumers need to filter by it

  Rules that hold up:

    * name the EVENT, in the past tense: orders.created, not orders.create
    * put the entity first, so a wildcard subscription is useful
    * keep ids OUT of the subject: orders.created.42 makes one subject per
      order, and the wildcard subscription that follows is unbounded
    * version in the payload, not the subject: orders.created.v2 forces every
      consumer to change subscription for a compatible field addition`)

	fmt.Println()
}
