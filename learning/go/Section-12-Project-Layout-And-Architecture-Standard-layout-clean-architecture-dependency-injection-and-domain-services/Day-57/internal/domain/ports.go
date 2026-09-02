package domain

import (
	"context"
	"time"
)

/*
Ports: the interfaces the outer layers implement.

They are declared here, in the inner layer, and satisfied out there. That
inversion is the whole trick of clean architecture - the database plugs into
the domain, not the other way round.
*/

// SubscriptionRepository is the storage port.
type SubscriptionRepository interface {
	Save(ctx context.Context, subscription Subscription) (Subscription, error)
	ByID(ctx context.Context, id int64) (Subscription, error)
	ByCustomer(ctx context.Context, customerID string) (Subscription, error)
	List(ctx context.Context, state SubscriptionState, limit int) ([]Subscription, error)
}

// EventPublisher is the notification port: the use cases announce what
// happened without knowing whether it becomes an email, a Kafka message or a
// line in a log.
type EventPublisher interface {
	Publish(ctx context.Context, event Event) error
}

type Event struct {
	Name           string
	SubscriptionID int64
	CustomerID     string
	Detail         string
}

// Clock is the time port. Business rules that depend on "now" are only
// testable if "now" can be supplied - a use case that calls time.Now() itself
// cannot be tested for anything that happens tomorrow.
type Clock interface {
	Now() time.Time
}
