// Command publisher emits order events. In production this is the API
// process; here it is separate so the two can be run in two terminals.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/events"
)

/*
	go run ./cmd/worker &
	go run ./cmd/publisher

Both need a broker:

	docker run --rm -p 4222:4222 nats:2.10-alpine -js
	export NATS_URL=nats://localhost:4222
*/

func main() {
	url := envOr("NATS_URL", "nats://127.0.0.1:4222")

	connection, js, err := events.Connect(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publisher: %v\n", err)
		os.Exit(1)
	}

	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := events.EnsureStream(ctx, js); err != nil {
		fmt.Fprintf(os.Stderr, "publisher: %v\n", err)
		os.Exit(1)
	}

	if _, err := events.EnsureDeadLetterStream(ctx, js); err != nil {
		fmt.Fprintf(os.Stderr, "publisher: %v\n", err)
		os.Exit(1)
	}

	publisher := events.NewPublisher(js)

	for i := 1; i <= 10; i++ {
		event, err := events.NewEvent(
			fmt.Sprintf("order-%d-created", i),
			"order.created",
			events.OrderCreated{OrderID: int64(i), Customer: "ada", AmountCent: int64(500 * i)},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "publisher: %v\n", err)
			os.Exit(1)
		}

		sequence, err := publisher.Publish(ctx, events.SubjectOrderCreated, event)
		if err != nil {
			fmt.Fprintf(os.Stderr, "publisher: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("published %s (sequence %d)\n", event.ID, sequence)

		time.Sleep(200 * time.Millisecond)
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
