// Package queue moves events from the outbox table to the workers.
//
// There are two pieces:
//
//	Relay - reads unpublished outbox rows, hands them to the Bus, marks them
//	        published. Publish first, mark second: a crash between the two
//	        redelivers the event, which is safe, while marking first would
//	        lose it, which is not.
//	Bus   - in-process pub/sub standing in for NATS or Kafka. Swapping it for
//	        a real broker changes this file and nothing else.
//
// The Bus can deliver every message twice on purpose. Duplicates are not a bug
// to be designed out of the transport - at-least-once delivery is what every
// real broker gives you - so the consumer has to be built for them, and a
// test has to prove it.
package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
)

// Delivery is one attempt at handing an event to a consumer.
type Delivery struct {
	EventID string
	Type    string
	Payload []byte
	Attempt int
}

type Handler func(ctx context.Context, delivery Delivery) error

type subscription struct {
	consumer string
	handler  Handler
}

type Bus struct {
	logger *slog.Logger

	mu          sync.Mutex
	subscribers map[string][]subscription

	// duplicates delivers each message this many extra times, to prove the
	// consumer's idempotency in the demo and in tests.
	duplicates int

	delivered atomic.Int64
	failures  atomic.Int64
}

func NewBus(logger *slog.Logger) *Bus {
	if logger == nil {
		logger = slog.Default()
	}

	return &Bus{logger: logger, subscribers: make(map[string][]subscription)}
}

// DeliverTwice turns on deliberate duplicate delivery.
func (b *Bus) DeliverTwice(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if on {
		b.duplicates = 1

		return
	}

	b.duplicates = 0
}

func (b *Bus) Subscribe(eventType, consumer string, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers[eventType] = append(b.subscribers[eventType], subscription{consumer: consumer, handler: handler})
}

func (b *Bus) Stats() (delivered, failures int64) {
	return b.delivered.Load(), b.failures.Load()
}

// Publish delivers to every subscriber of the event type.
func (b *Bus) Publish(ctx context.Context, event store.Event) error {
	b.mu.Lock()

	subscribers := append([]subscription(nil), b.subscribers[event.Type]...)
	attempts := 1 + b.duplicates

	b.mu.Unlock()

	for _, subscriber := range subscribers {
		for attempt := 1; attempt <= attempts; attempt++ {
			delivery := Delivery{
				EventID: event.EventID,
				Type:    event.Type,
				Payload: event.Payload,
				Attempt: attempt,
			}

			b.delivered.Add(1)

			if err := subscriber.handler(ctx, delivery); err != nil {
				b.failures.Add(1)

				b.logger.Error("handler failed",
					slog.String("consumer", subscriber.consumer),
					slog.String("event_id", event.EventID),
					slog.String("error", err.Error()))

				// The publish fails so the relay leaves the row unpublished
				// and tries again on the next tick.
				return fmt.Errorf("deliver %s to %s: %w", event.EventID, subscriber.consumer, err)
			}
		}
	}

	return nil
}

// Publisher is the seam a real broker plugs into.
type Publisher interface {
	Publish(ctx context.Context, event store.Event) error
}

type Relay struct {
	store     *store.Store
	publisher Publisher
	logger    *slog.Logger
	interval  time.Duration

	published atomic.Int64
}

func NewRelay(s *store.Store, publisher Publisher, interval time.Duration, logger *slog.Logger) *Relay {
	if logger == nil {
		logger = slog.Default()
	}

	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	return &Relay{store: s, publisher: publisher, logger: logger, interval: interval}
}

func (r *Relay) Published() int64 {
	return r.published.Load()
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			if err := r.DrainOnce(ctx); err != nil && ctx.Err() == nil {
				r.logger.Error("relay drain failed", slog.String("error", err.Error()))
			}
		}
	}
}

// DrainOnce publishes everything pending, oldest first.
func (r *Relay) DrainOnce(ctx context.Context) error {
	events, err := r.store.UnpublishedEvents(ctx, 100)
	if err != nil {
		return err
	}

	for _, event := range events {
		if err := r.publisher.Publish(ctx, event); err != nil {
			if recordErr := r.store.RecordPublishFailure(ctx, event.ID); recordErr != nil {
				r.logger.Error("record failure", slog.String("error", recordErr.Error()))
			}

			// Stop here: publishing the next event first would let a consumer
			// see an update before the create.
			return err
		}

		if err := r.store.MarkPublished(ctx, event.ID); err != nil {
			return err
		}

		r.published.Add(1)
	}

	return nil
}
