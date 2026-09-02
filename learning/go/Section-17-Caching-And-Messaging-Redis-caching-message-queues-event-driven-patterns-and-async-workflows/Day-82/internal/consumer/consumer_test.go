package consumer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/broker"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/consumer"
)

// TestDuplicateDeliveriesSendOneEmail is the whole point of the day: the same
// event arriving three times must produce one side effect.
func TestDuplicateDeliveriesSendOneEmail(t *testing.T) {
	t.Parallel()

	sender := consumer.NewEmailSender(0)
	handler := consumer.NewWelcomeHandler(consumer.NewDeduplicator(time.Hour), sender)

	message := func() *broker.Message {
		return &broker.Message{
			ID:      "delivery-1",
			Topic:   "users.registered",
			Payload: []byte("ada@example.com"),
			Headers: map[string]string{"event-id": "user-42"},
		}
	}

	for range 3 {
		handler.Handle(context.Background(), message())
	}

	if sent := sender.Sent(); len(sent) != 1 {
		t.Fatalf("sent %d emails, want 1: %v", len(sent), sent)
	}

	processed, skipped, failed := handler.Counts()

	if processed != 1 || skipped != 2 || failed != 0 {
		t.Fatalf("processed=%d skipped=%d failed=%d, want 1/2/0", processed, skipped, failed)
	}
}

// TestDifferentEventsAreNotDeduplicated: the key must identify the EVENT, not
// just "some message", or real work gets silently dropped.
func TestDifferentEventsAreNotDeduplicated(t *testing.T) {
	t.Parallel()

	sender := consumer.NewEmailSender(0)
	handler := consumer.NewWelcomeHandler(consumer.NewDeduplicator(time.Hour), sender)

	for _, event := range []struct{ id, address string }{
		{"user-1", "ada@example.com"},
		{"user-2", "alan@example.com"},
		{"user-3", "grace@example.com"},
	} {
		handler.Handle(context.Background(), &broker.Message{
			ID:      "delivery",
			Payload: []byte(event.address),
			Headers: map[string]string{"event-id": event.id},
		})
	}

	if sent := sender.Sent(); len(sent) != 3 {
		t.Fatalf("sent %d emails, want 3: %v", len(sent), sent)
	}
}

// TestFailedWorkCanBeRetried: a handler that claims a key and then fails must
// release the claim, or the retry is deduplicated away and the email is never
// sent at all.
func TestFailedWorkCanBeRetried(t *testing.T) {
	t.Parallel()

	// The first two sends fail, the third succeeds.
	sender := consumer.NewEmailSender(2)
	deduplicator := consumer.NewDeduplicator(time.Hour)
	handler := consumer.NewWelcomeHandler(deduplicator, sender)

	for range 3 {
		handler.Handle(context.Background(), &broker.Message{
			ID:      "delivery",
			Payload: []byte("ada@example.com"),
			Headers: map[string]string{"event-id": "user-42"},
		})
	}

	if sent := sender.Sent(); len(sent) != 1 {
		t.Fatalf("sent %v, want exactly one successful email", sent)
	}

	processed, _, failed := handler.Counts()

	if processed != 1 || failed != 2 {
		t.Fatalf("processed=%d failed=%d, want 1 and 2", processed, failed)
	}
}

// TestEndToEndWithRedelivery runs the handler behind the broker, so the
// deduplication is exercised against real redeliveries rather than a loop.
func TestEndToEndWithRedelivery(t *testing.T) {
	t.Parallel()

	config := broker.DefaultConfig()
	config.AckWait = 100 * time.Millisecond
	config.MaxDeliveries = 5

	ctx, cancel := context.WithCancel(context.Background())

	bus := broker.New(config)

	t.Cleanup(func() {
		cancel()

		if err := bus.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	// The first attempt fails at the SMTP layer, so the broker redelivers.
	sender := consumer.NewEmailSender(1)
	handler := consumer.NewWelcomeHandler(consumer.NewDeduplicator(time.Hour), sender)

	var deliveries sync.WaitGroup

	deliveries.Add(2)

	bus.Subscribe(ctx, "users.registered", "mailer", func(ctx context.Context, message *broker.Message) {
		handler.Handle(ctx, message)

		deliveries.Done()
	})

	if _, err := bus.Publish(ctx, "users.registered", "user-42",
		[]byte("ada@example.com"), map[string]string{"event-id": "user-42"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, &deliveries, 5*time.Second)

	if sent := sender.Sent(); len(sent) != 1 {
		t.Fatalf("sent %v, want one email despite the redelivery", sent)
	}
}

func TestPoisonMessageIsTerminated(t *testing.T) {
	t.Parallel()

	sender := consumer.NewEmailSender(0)
	handler := consumer.NewWelcomeHandler(consumer.NewDeduplicator(time.Hour), sender)

	message := &broker.Message{
		ID:      "delivery",
		Payload: []byte(""), // no address: it will fail identically forever
		Headers: map[string]string{"event-id": "user-99"},
	}

	handler.Handle(context.Background(), message)

	_, _, failed := handler.Counts()

	if failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}

	if len(sender.Sent()) != 0 {
		t.Fatal("an email was sent for a message with no address")
	}
}

func TestDeduplicatorTTL(t *testing.T) {
	t.Parallel()

	deduplicator := consumer.NewDeduplicator(50 * time.Millisecond)

	if !deduplicator.Claim("event-1") {
		t.Fatal("the first claim was rejected")
	}

	if deduplicator.Claim("event-1") {
		t.Fatal("the second claim was accepted within the TTL")
	}

	time.Sleep(80 * time.Millisecond)

	// After the TTL the key is forgotten. That is the trade: a redelivery
	// arriving later than the TTL WILL be processed twice, so the TTL must
	// exceed the broker's maximum redelivery window.
	if !deduplicator.Claim("event-1") {
		t.Fatal("the claim was still held after the TTL expired")
	}
}

func TestConcurrentDeliveriesClaimOnce(t *testing.T) {
	t.Parallel()

	deduplicator := consumer.NewDeduplicator(time.Hour)

	var (
		waitGroup sync.WaitGroup
		mu        sync.Mutex
		winners   int
	)

	for range 50 {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			if deduplicator.Claim("same-event") {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}

	waitGroup.Wait()

	if winners != 1 {
		t.Fatalf("%d goroutines claimed the same key, want 1", winners)
	}
}

func waitFor(t *testing.T, group *sync.WaitGroup, timeout time.Duration) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		group.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for deliveries")
	}
}
