// Package bus is a small in-process broker with redelivery and a dead letter
// queue. Day 83 did this with real NATS JetStream; here the point is not the
// transport but the LIFECYCLE of a message that keeps failing.
//
// The lifecycle:
//
//	deliver -> handler fails -> redeliver -> fails -> ... -> give up -> DLQ
//
// The DLQ is the answer to a question every queue eventually asks: what do we
// do with a message we cannot process? Retrying forever blocks the queue
// behind it (head-of-line blocking) and burns capacity on a message that will
// never succeed. Dropping it loses data silently. The DLQ does neither: the
// message leaves the hot path and lands somewhere a human can look at it.
package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/outbox"
)

// Delivery is what a handler receives.
type Delivery struct {
	EventID     string
	Type        string
	Payload     json.RawMessage
	Attempt     int // 1 on the first delivery
	Redelivered bool
}

// Handler processes a delivery. Returning an error means "redeliver me".
type Handler func(ctx context.Context, delivery Delivery) error

// DeadLetter is a message that exhausted its attempts.
type DeadLetter struct {
	Delivery Delivery
	Reason   string
	At       time.Time
}

type subscription struct {
	name    string
	handler Handler
}

// Bus delivers events to subscribers, synchronously, with retries.
//
// Synchronous delivery is a deliberate teaching choice: it makes the demo and
// the tests deterministic. A real broker delivers asynchronously, which is why
// consumers need the deduplication from the idempotency package.
type Bus struct {
	logger *slog.Logger

	// maxAttempts includes the first delivery, so 3 means one try plus two
	// redeliveries.
	maxAttempts int
	backoff     time.Duration

	mu          sync.Mutex
	subscribers map[string][]subscription
	dead        []DeadLetter
	failures    map[string]bool // eventType -> publish rejected
}

func New(logger *slog.Logger, maxAttempts int) *Bus {
	if logger == nil {
		logger = slog.Default()
	}

	if maxAttempts < 1 {
		maxAttempts = 1
	}

	return &Bus{
		logger:      logger,
		maxAttempts: maxAttempts,
		backoff:     5 * time.Millisecond,
		subscribers: make(map[string][]subscription),
		failures:    make(map[string]bool),
	}
}

func (b *Bus) Subscribe(eventType, name string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[eventType] = append(b.subscribers[eventType], subscription{name: name, handler: handler})
}

// Break makes publishing fail, which is how the demo shows the outbox holding
// events safely while the broker is down.
func (b *Bus) Break(eventType string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures[eventType] = true
}

func (b *Bus) Heal(eventType string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.failures, eventType)
}

func (b *Bus) DeadLetters() []DeadLetter {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]DeadLetter(nil), b.dead...)
}

// Publish satisfies outbox.Publisher.
func (b *Bus) Publish(ctx context.Context, eventType string, event outbox.Event) error {
	b.mu.Lock()

	broken := b.failures[eventType]
	subscribers := append([]subscription(nil), b.subscribers[eventType]...)

	b.mu.Unlock()

	if broken {
		return fmt.Errorf("broker unavailable for %s", eventType)
	}

	for _, subscriber := range subscribers {
		b.deliver(ctx, subscriber, Delivery{
			EventID: event.EventID,
			Type:    eventType,
			Payload: event.Payload,
		})
	}

	return nil
}

// deliver retries one subscriber, then dead-letters.
//
// Note that a failure for one subscriber does not fail the publish: the
// producer's job ended when the broker accepted the event. Consumer failures
// are the consumer's problem, and the DLQ is where they end up.
func (b *Bus) deliver(ctx context.Context, subscriber subscription, delivery Delivery) {
	var lastErr error

	for attempt := 1; attempt <= b.maxAttempts; attempt++ {
		delivery.Attempt = attempt
		delivery.Redelivered = attempt > 1

		lastErr = subscriber.handler(ctx, delivery)
		if lastErr == nil {
			return
		}

		b.logger.Warn("delivery failed",
			slog.String("consumer", subscriber.name),
			slog.String("event_id", delivery.EventID),
			slog.Int("attempt", attempt),
			slog.String("error", lastErr.Error()))

		if attempt < b.maxAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(b.backoff * time.Duration(attempt)):
			}
		}
	}

	b.mu.Lock()
	b.dead = append(b.dead, DeadLetter{
		Delivery: delivery,
		Reason:   fmt.Sprintf("%s: %v", subscriber.name, lastErr),
		At:       time.Now(),
	})
	b.mu.Unlock()

	b.logger.Error("dead lettered",
		slog.String("consumer", subscriber.name),
		slog.String("event_id", delivery.EventID),
		slog.Int("attempts", b.maxAttempts))
}

// Redrive replays dead letters back through the bus - what an operator does
// after fixing the bug that caused them. The DLQ is a queue, not a graveyard.
func (b *Bus) Redrive(ctx context.Context) int {
	b.mu.Lock()

	pending := b.dead
	b.dead = nil

	b.mu.Unlock()

	for _, letter := range pending {
		b.mu.Lock()
		subscribers := append([]subscription(nil), b.subscribers[letter.Delivery.Type]...)
		b.mu.Unlock()

		for _, subscriber := range subscribers {
			b.deliver(ctx, subscriber, Delivery{
				EventID: letter.Delivery.EventID,
				Type:    letter.Delivery.Type,
				Payload: letter.Delivery.Payload,
			})
		}
	}

	return len(pending)
}
