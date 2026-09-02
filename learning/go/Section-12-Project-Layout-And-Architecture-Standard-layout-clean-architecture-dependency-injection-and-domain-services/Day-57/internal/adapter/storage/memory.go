// Package storage is a driven adapter: it implements the repository port
// declared in the domain.
//
// It imports the domain and nothing else from this service. A Postgres
// version would sit next to this file and be selected in main.
package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/domain"
)

type MemorySubscriptions struct {
	mu            sync.RWMutex
	subscriptions map[int64]domain.Subscription
	nextID        int64
}

func NewMemorySubscriptions() *MemorySubscriptions {
	return &MemorySubscriptions{
		subscriptions: make(map[int64]domain.Subscription),
		nextID:        1,
	}
}

var _ domain.SubscriptionRepository = (*MemorySubscriptions)(nil)

// Save is an upsert: the use case does not care whether the entity is new,
// which keeps "create" and "update" out of the port's vocabulary.
func (r *MemorySubscriptions) Save(ctx context.Context, subscription domain.Subscription) (domain.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if subscription.ID == 0 {
		subscription.ID = r.nextID
		r.nextID++
	}

	r.subscriptions[subscription.ID] = subscription

	return subscription, nil
}

func (r *MemorySubscriptions) ByID(ctx context.Context, id int64) (domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subscription, found := r.subscriptions[id]
	if !found {
		return domain.Subscription{}, fmt.Errorf("subscription %d: %w", id, domain.ErrNotFound)
	}

	return subscription, nil
}

func (r *MemorySubscriptions) ByCustomer(ctx context.Context, customerID string) (domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Newest first: a customer who resubscribed has more than one row.
	found := make([]domain.Subscription, 0, 2)

	for _, subscription := range r.subscriptions {
		if subscription.CustomerID == customerID {
			found = append(found, subscription)
		}
	}

	if len(found) == 0 {
		return domain.Subscription{}, fmt.Errorf("customer %s: %w", customerID, domain.ErrNotFound)
	}

	sort.Slice(found, func(i, j int) bool { return found[i].ID > found[j].ID })

	return found[0], nil
}

func (r *MemorySubscriptions) List(ctx context.Context, state domain.SubscriptionState, limit int) ([]domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]domain.Subscription, 0, len(r.subscriptions))

	for _, subscription := range r.subscriptions {
		if state != "" && subscription.State != state {
			continue
		}

		matched = append(matched, subscription)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	return matched, nil
}

//
// OTHER ADAPTERS FOR THE REMAINING PORTS
//

// LogPublisher implements the event port by writing lines. Swapping it for
// NATS (Day 83) changes this file only.
type LogPublisher struct {
	mu     sync.Mutex
	events []domain.Event
	Printf func(format string, args ...any)
}

func NewLogPublisher(printf func(format string, args ...any)) *LogPublisher {
	return &LogPublisher{Printf: printf}
}

var _ domain.EventPublisher = (*LogPublisher)(nil)

func (p *LogPublisher) Publish(ctx context.Context, event domain.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, event)

	if p.Printf != nil {
		p.Printf("event=%s subscription=%d customer=%s detail=%s",
			event.Name, event.SubscriptionID, event.CustomerID, event.Detail)
	}

	return nil
}

func (p *LogPublisher) Events() []domain.Event {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]domain.Event(nil), p.events...)
}
