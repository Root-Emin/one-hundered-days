package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/broker"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/consumer"
)

/*
Day 82 - Caching & Messaging: Message Queues Overview

Tasks covered:

 1. The concepts, run rather than described: producers, consumers, topics,
    queue groups, acknowledgments, redelivery, dead letters
 2. Brokers compared, with the trade-off each one is actually making
 3. Async work identified: which jobs belong off the request path, and why
 4. Idempotent consumers, demonstrated by delivering the same event twice

Files:

	internal/broker    a teaching broker: ack, nack, redelivery, dead letter
	internal/consumer  a deduplicating handler, the shape every consumer needs

Run:

	go run .

Test:

	go test -race ./...
*/

func main() {
	demonstrateWorkQueue()
	demonstratePubSub()
	demonstrateRedelivery()
	demonstrateIdempotency()
	demonstrateDeadLetter()

	printBrokers()
	printUseCases()
	printDeliveryGuarantees()
}

func demonstrateWorkQueue() {
	fmt.Println("\n1) Work queue: one message, one consumer")
	fmt.Println(strings.Repeat("-", 80))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := broker.New(broker.DefaultConfig())

	var (
		mu       sync.Mutex
		handled  = map[string]int{}
		complete sync.WaitGroup
	)

	complete.Add(6)

	// Three consumers in ONE group: they share the work.
	for worker := 1; worker <= 3; worker++ {
		name := fmt.Sprintf("worker-%d", worker)

		bus.Subscribe(ctx, "jobs", "workers", func(ctx context.Context, message *broker.Message) {
			mu.Lock()
			handled[name]++
			mu.Unlock()

			message.Ack()
			complete.Done()
		})
	}

	for i := range 6 {
		if _, err := bus.Publish(ctx, "jobs", "", []byte(fmt.Sprintf("job %d", i+1)), nil); err != nil {
			fmt.Printf("  publish: %v\n", err)
		}
	}

	complete.Wait()

	mu.Lock()
	total := 0

	for name, count := range handled {
		fmt.Printf("  %s handled %d\n", name, count)

		total += count
	}

	mu.Unlock()

	fmt.Printf("  6 messages, %d handled in total: each went to exactly ONE consumer\n", total)
	fmt.Println("  Scaling out means adding consumers to the group - no code change.")
}

func demonstratePubSub() {
	fmt.Println("\n2) Pub/sub: one message, every group")
	fmt.Println(strings.Repeat("-", 80))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := broker.New(broker.DefaultConfig())

	var complete sync.WaitGroup

	complete.Add(3)

	// Three DIFFERENT groups: each receives its own copy.
	for _, group := range []string{"email", "analytics", "search-index"} {
		name := group

		bus.Subscribe(ctx, "orders.created", name, func(ctx context.Context, message *broker.Message) {
			fmt.Printf("  %-14s received %q\n", name, message.Payload)

			message.Ack()
			complete.Done()
		})
	}

	if _, err := bus.Publish(ctx, "orders.created", "order-1", []byte("order 1 created"), nil); err != nil {
		fmt.Printf("  publish: %v\n", err)
	}

	complete.Wait()

	fmt.Println("  One publish, three independent consumers. The producer knows about")
	fmt.Println("  none of them - which is what lets a fourth be added tomorrow.")
}

func demonstrateRedelivery() {
	fmt.Println("\n3) Acknowledgment and redelivery")
	fmt.Println(strings.Repeat("-", 80))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := broker.DefaultConfig()
	config.AckWait = 200 * time.Millisecond
	config.MaxDeliveries = 3

	bus := broker.New(config)

	var (
		deliveries []int
		mu         sync.Mutex
		done       = make(chan struct{})
	)

	bus.Subscribe(ctx, "flaky", "workers", func(ctx context.Context, message *broker.Message) {
		mu.Lock()
		deliveries = append(deliveries, message.Deliveries)
		attempt := len(deliveries)
		mu.Unlock()

		switch attempt {
		case 1:
			fmt.Printf("  delivery %d: handler nacks (a transient failure)\n", message.Deliveries)
			message.Nack()

		case 2:
			fmt.Printf("  delivery %d: handler never acks (a crashed consumer)\n", message.Deliveries)
			// No ack at all: the broker's AckWait expires and it redelivers.

		default:
			fmt.Printf("  delivery %d: handler acks\n", message.Deliveries)
			message.Ack()

			close(done)
		}
	})

	if _, err := bus.Publish(ctx, "flaky", "", []byte("work"), nil); err != nil {
		fmt.Printf("  publish: %v\n", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		fmt.Println("  (timed out)")
	}

	fmt.Println("\n  One publish, three deliveries. This is AT LEAST ONCE: the broker")
	fmt.Println("  would rather deliver twice than lose a message, so the consumer")
	fmt.Println("  has to be safe to run twice.")
}

func demonstrateIdempotency() {
	fmt.Println("\n4) The same event delivered twice")
	fmt.Println(strings.Repeat("-", 80))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := broker.New(broker.DefaultConfig())

	sender := consumer.NewEmailSender(0)
	handler := consumer.NewWelcomeHandler(consumer.NewDeduplicator(time.Hour), sender)

	var complete sync.WaitGroup

	complete.Add(3)

	bus.Subscribe(ctx, "users.registered", "mailer", func(ctx context.Context, message *broker.Message) {
		handler.Handle(ctx, message)
		complete.Done()
	})

	// The same logical event, published three times - a producer retry, a
	// broker redelivery, and a replay after an incident.
	headers := map[string]string{"event-id": "user-42-registered"}

	for range 3 {
		if _, err := bus.Publish(ctx, "users.registered", "user-42", []byte("ada@example.com"), headers); err != nil {
			fmt.Printf("  publish: %v\n", err)
		}
	}

	complete.Wait()

	processed, skipped, failed := handler.Counts()

	fmt.Printf("  3 deliveries -> processed=%d skipped=%d failed=%d\n", processed, skipped, failed)
	fmt.Printf("  emails actually sent: %d to %v\n", len(sender.Sent()), sender.Sent())
	fmt.Println("\n  The dedup key comes from the EVENT (a header the producer sets), not")
	fmt.Println("  from the delivery. A key derived from the delivery would be different")
	fmt.Println("  every time and would deduplicate nothing.")
}

func demonstrateDeadLetter() {
	fmt.Println("\n5) Poison messages and the dead letter queue")
	fmt.Println(strings.Repeat("-", 80))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := broker.DefaultConfig()
	config.AckWait = 200 * time.Millisecond
	config.MaxDeliveries = 3

	bus := broker.New(config)

	dead := make(chan *broker.Message, 4)

	bus.Subscribe(ctx, "dead-letter", "inspector", func(ctx context.Context, message *broker.Message) {
		dead <- message

		message.Ack()
	})

	// A handler that can never succeed with this input.
	bus.Subscribe(ctx, "payments", "workers", func(ctx context.Context, message *broker.Message) {
		message.Nack()
	})

	if _, err := bus.Publish(ctx, "payments", "", []byte("{malformed"), nil); err != nil {
		fmt.Printf("  publish: %v\n", err)
	}

	select {
	case message := <-dead:
		fmt.Printf("  after %s deliveries the message was dead-lettered\n",
			message.Headers["delivery-count"])
		fmt.Printf("  payload preserved: %q, original topic: %s\n",
			message.Payload, message.Headers["original-topic"])

	case <-time.After(3 * time.Second):
		fmt.Println("  (timed out)")
	}

	stats := bus.Stats()

	fmt.Printf("\n  broker stats: published=%d delivered=%d acked=%d requeued=%d dead=%d\n",
		stats.Published, stats.Delivered, stats.Acked, stats.Requeued, stats.DeadLetter)
	fmt.Println("  Without MaxDeliveries this message would be retried forever, and one")
	fmt.Println("  bad payload would occupy a consumer permanently.")
}

func printBrokers() {
	fmt.Println("\n6) Choosing a broker")
	fmt.Println(strings.Repeat("-", 80))

	rows := []struct {
		broker string
		model  string
		best   string
		cost   string
	}{
		{"NATS (core)", "pub/sub, at-most-once", "low-latency fan-out, service discovery", "no persistence: a restart loses in-flight messages"},
		{"NATS JetStream", "streams, at-least-once", "most Go services: simple, fast, durable", "younger ecosystem than the alternatives"},
		{"RabbitMQ", "queues + exchanges", "complex routing, per-message TTL, priorities", "an Erlang cluster to operate"},
		{"Kafka", "partitioned log", "high throughput, replay, event sourcing", "operationally heavy; ordering only within a partition"},
		{"AWS SQS", "managed queue", "nothing to run; simple work queues", "no fan-out (that is SNS), at-least-once, visibility timeouts"},
		{"Postgres table", "a queue in the database", "low volume, and you already have Postgres", "polling, and it competes with your own load"},
	}

	fmt.Printf("  %-16s %-24s %-40s %s\n", "BROKER", "MODEL", "BEST AT", "COST")

	for _, row := range rows {
		fmt.Printf("  %-16s %-24s %-40s %s\n", row.broker, row.model, row.best, row.cost)
	}

	fmt.Println("\n  The honest first question is whether a broker is needed at all. A")
	fmt.Println("  goroutine and a channel handle plenty of \"do this later\" work - until")
	fmt.Println("  the process restarts and the work is gone. Durability is what you are")
	fmt.Println("  actually buying.")
}

func printUseCases() {
	fmt.Println("\n7) What belongs off the request path")
	fmt.Println(strings.Repeat("-", 80))

	rows := []struct {
		work  string
		async string
		why   string
	}{
		{"Send a welcome email", "yes", "slow, external, and the user does not wait for it"},
		{"Generate a PDF invoice", "yes", "seconds of CPU; the response should not block on it"},
		{"Update a search index", "yes", "eventual consistency is fine for search"},
		{"Deliver a webhook", "yes", "the receiver may be down; retries need a queue"},
		{"Resize an uploaded image", "yes", "CPU-heavy, and a placeholder can be shown meanwhile"},
		{"Recalculate analytics", "yes", "nobody is waiting on the request"},
		{"Charge a card at checkout", "NO", "the user must be told immediately whether it worked"},
		{"Validate a login", "NO", "the answer IS the response"},
		{"Reserve the last item in stock", "NO", "a race the user must not lose silently"},
	}

	fmt.Printf("  %-34s %-8s %s\n", "WORK", "ASYNC?", "WHY")

	for _, row := range rows {
		fmt.Printf("  %-34s %-8s %s\n", row.work, row.async, row.why)
	}

	fmt.Println("\n  The test: if the user needs the answer to continue, it is synchronous.")
	fmt.Println("  If they only need to know it was accepted, it can be a message.")
}

func printDeliveryGuarantees() {
	fmt.Println("\n8) Delivery guarantees")
	fmt.Println(strings.Repeat("-", 80))

	fmt.Println(`  AT MOST ONCE     ack before processing. Fast, and a crash loses the
                   message. Acceptable for metrics, never for money.

  AT LEAST ONCE    ack after processing. A crash between the work and the ack
                   causes a redelivery, so the consumer MUST be idempotent.
                   This is what almost everything uses, including this broker.

  EXACTLY ONCE     does not exist end to end. What vendors call exactly-once
                   is at-least-once delivery plus deduplication inside their
                   own boundary. The moment a side effect leaves that boundary
                   (an email, a card charge), you are back to at-least-once
                   and your own dedup key.

  Which means the real design question is never "which guarantee?" but:

      what is the stable key that identifies this event,
      and where do I record that I have handled it?`)

	fmt.Println()
}
