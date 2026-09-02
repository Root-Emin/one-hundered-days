package broker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/broker"
)

func newBroker(t *testing.T, config broker.Config) (*broker.Broker, context.Context) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	bus := broker.New(config)

	t.Cleanup(func() {
		cancel()

		if err := bus.Close(); err != nil {
			t.Errorf("close broker: %v", err)
		}
	})

	return bus, ctx
}

// TestQueueGroupSharesWork: consumers in one group must not each get a copy,
// or every job would run N times.
func TestQueueGroupSharesWork(t *testing.T) {
	t.Parallel()

	bus, ctx := newBroker(t, broker.DefaultConfig())

	const messages = 20

	var (
		handled atomic.Int64
		done    sync.WaitGroup
	)

	done.Add(messages)

	for range 4 {
		bus.Subscribe(ctx, "jobs", "workers", func(ctx context.Context, message *broker.Message) {
			handled.Add(1)

			message.Ack()
			done.Done()
		})
	}

	for i := range messages {
		if _, err := bus.Publish(ctx, "jobs", "", []byte(fmt.Sprint(i)), nil); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	done.Wait()

	if handled.Load() != messages {
		t.Fatalf("handled %d, want %d - a message went to more than one consumer in the group",
			handled.Load(), messages)
	}
}

// TestEveryGroupGetsACopy is the other half: separate groups are separate
// subscribers, and each must see every message.
func TestEveryGroupGetsACopy(t *testing.T) {
	t.Parallel()

	bus, ctx := newBroker(t, broker.DefaultConfig())

	groups := []string{"email", "analytics", "audit"}

	var (
		mu   sync.Mutex
		seen = map[string]int{}
		done sync.WaitGroup
	)

	done.Add(len(groups))

	for _, group := range groups {
		name := group

		bus.Subscribe(ctx, "orders", name, func(ctx context.Context, message *broker.Message) {
			mu.Lock()
			seen[name]++
			mu.Unlock()

			message.Ack()
			done.Done()
		})
	}

	if _, err := bus.Publish(ctx, "orders", "", []byte("created"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	done.Wait()

	mu.Lock()
	defer mu.Unlock()

	for _, group := range groups {
		if seen[group] != 1 {
			t.Errorf("group %q received %d copies, want 1", group, seen[group])
		}
	}
}

// TestNackRedelivers: a failed message must come back.
func TestNackRedelivers(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 100 * time.Millisecond
	config.MaxDeliveries = 5

	bus, ctx := newBroker(t, config)

	var (
		attempts atomic.Int64
		done     = make(chan int, 1)
	)

	bus.Subscribe(ctx, "flaky", "workers", func(ctx context.Context, message *broker.Message) {
		attempt := attempts.Add(1)

		if attempt < 3 {
			message.Nack()

			return
		}

		message.Ack()

		select {
		case done <- message.Deliveries:
		default:
		}
	})

	if _, err := bus.Publish(ctx, "flaky", "", []byte("work"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case deliveries := <-done:
		if deliveries != 3 {
			t.Fatalf("succeeded on delivery %d, want 3", deliveries)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("the message was never redelivered")
	}
}

// TestMissingAckRedelivers is the crashed-consumer case: no ack, no nack, and
// the broker must not lose the message.
func TestMissingAckRedelivers(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 100 * time.Millisecond
	config.MaxDeliveries = 4

	bus, ctx := newBroker(t, config)

	deliveries := make(chan int, 4)

	bus.Subscribe(ctx, "silent", "workers", func(ctx context.Context, message *broker.Message) {
		deliveries <- message.Deliveries

		if message.Deliveries >= 2 {
			message.Ack()
		}
		// On the first delivery: no ack at all, as if the consumer died.
	})

	if _, err := bus.Publish(ctx, "silent", "", []byte("work"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	first := <-deliveries

	select {
	case second := <-deliveries:
		if first != 1 || second != 2 {
			t.Fatalf("deliveries = %d then %d, want 1 then 2", first, second)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("an unacked message was never redelivered - it would be lost")
	}
}

// TestDeadLetterAfterMaxDeliveries: a poison message must stop being retried.
func TestDeadLetterAfterMaxDeliveries(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 50 * time.Millisecond
	config.MaxDeliveries = 3
	config.DeadLetterTopic = "dead-letter"

	bus, ctx := newBroker(t, config)

	dead := make(chan *broker.Message, 2)

	bus.Subscribe(ctx, "dead-letter", "inspector", func(ctx context.Context, message *broker.Message) {
		dead <- message

		message.Ack()
	})

	var attempts atomic.Int64

	bus.Subscribe(ctx, "poison", "workers", func(ctx context.Context, message *broker.Message) {
		attempts.Add(1)

		message.Nack()
	})

	if _, err := bus.Publish(ctx, "poison", "", []byte("bad"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case message := <-dead:
		if message.Headers["original-topic"] != "poison" {
			t.Fatalf("headers = %v, want the original topic recorded", message.Headers)
		}

		if string(message.Payload) != "bad" {
			t.Fatalf("payload = %q, want it preserved for inspection", message.Payload)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("the poison message was never dead-lettered")
	}

	// Give any stray redelivery a chance to appear.
	time.Sleep(200 * time.Millisecond)

	if attempts.Load() > int64(config.MaxDeliveries) {
		t.Fatalf("delivered %d times, want at most %d", attempts.Load(), config.MaxDeliveries)
	}
}

// TestTermSkipsRetries: a message that can never succeed should go straight to
// the dead letter queue rather than burning the retry budget.
func TestTermSkipsRetries(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 100 * time.Millisecond
	config.MaxDeliveries = 5

	bus, ctx := newBroker(t, config)

	dead := make(chan *broker.Message, 2)

	bus.Subscribe(ctx, "dead-letter", "inspector", func(ctx context.Context, message *broker.Message) {
		dead <- message

		message.Ack()
	})

	var attempts atomic.Int64

	bus.Subscribe(ctx, "malformed", "workers", func(ctx context.Context, message *broker.Message) {
		attempts.Add(1)

		message.Term()
	})

	if _, err := bus.Publish(ctx, "malformed", "", []byte("{"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-dead:
	case <-time.After(3 * time.Second):
		t.Fatal("terminated message never reached the dead letter topic")
	}

	if attempts.Load() != 1 {
		t.Fatalf("delivered %d times, want 1: Term must not retry", attempts.Load())
	}
}

// TestPanicIsTreatedAsFailure: one bad message must not take the consumer down.
func TestPanicIsTreatedAsFailure(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 100 * time.Millisecond
	config.MaxDeliveries = 2

	bus, ctx := newBroker(t, config)

	var attempts atomic.Int64

	survived := make(chan struct{}, 1)

	bus.Subscribe(ctx, "panics", "workers", func(ctx context.Context, message *broker.Message) {
		if attempts.Add(1) == 1 {
			panic("handler exploded")
		}

		message.Ack()

		select {
		case survived <- struct{}{}:
		default:
		}
	})

	if _, err := bus.Publish(ctx, "panics", "", []byte("work"), nil); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-survived:
	case <-time.After(3 * time.Second):
		t.Fatal("the consumer did not survive a panicking handler")
	}
}

func TestStatsAndPending(t *testing.T) {
	t.Parallel()

	bus, ctx := newBroker(t, broker.DefaultConfig())

	var done sync.WaitGroup

	done.Add(3)

	bus.Subscribe(ctx, "counted", "workers", func(ctx context.Context, message *broker.Message) {
		message.Ack()
		done.Done()
	})

	for range 3 {
		if _, err := bus.Publish(ctx, "counted", "", []byte("x"), nil); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	done.Wait()

	// Give the acks a moment to be recorded.
	time.Sleep(50 * time.Millisecond)

	stats := bus.Stats()

	if stats.Published != 3 || stats.Acked != 3 {
		t.Fatalf("stats = %+v, want 3 published and 3 acked", stats)
	}

	if pending := bus.Pending("counted"); pending != 0 {
		t.Fatalf("pending = %d after everything was acked, want 0", pending)
	}
}
