// Package usecase holds the application layer: one type per group of use
// cases, orchestrating domain entities through the ports.
//
// It imports the domain (inward) and nothing else from this service. It does
// not know that HTTP exists, and it does not know which database is behind
// the repository port.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/domain"
)

const defaultTrial = 14 * 24 * time.Hour

type Subscriptions struct {
	repository domain.SubscriptionRepository
	events     domain.EventPublisher
	clock      domain.Clock
}

// NewSubscriptions takes ports, never concrete types. Everything this use case
// needs is visible in one line.
func NewSubscriptions(
	repository domain.SubscriptionRepository,
	events domain.EventPublisher,
	clock domain.Clock,
) *Subscriptions {
	return &Subscriptions{repository: repository, events: events, clock: clock}
}

// Subscribe is a use case: several domain operations and one storage write,
// composed into an application-level action.
func (s *Subscriptions) Subscribe(ctx context.Context, customerID string, plan domain.Plan, withTrial bool) (domain.Subscription, error) {
	existing, err := s.repository.ByCustomer(ctx, customerID)

	switch {
	case err == nil && existing.State != domain.StateCanceled:
		return domain.Subscription{}, fmt.Errorf("%w: customer %s already subscribed (id %d)",
			domain.ErrAlreadyExists, customerID, existing.ID)

	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return domain.Subscription{}, fmt.Errorf("subscribe %s: %w", customerID, err)
	}

	trial := time.Duration(0)
	if withTrial {
		trial = defaultTrial
	}

	// The entity constructor enforces the invariants; the use case only
	// decides which inputs to give it.
	subscription, err := domain.NewSubscription(customerID, plan, s.clock.Now(), trial)
	if err != nil {
		return domain.Subscription{}, err
	}

	saved, err := s.repository.Save(ctx, subscription)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("subscribe %s: %w", customerID, err)
	}

	s.publish(ctx, domain.Event{
		Name:           "subscription.created",
		SubscriptionID: saved.ID,
		CustomerID:     saved.CustomerID,
		Detail:         string(saved.Plan),
	})

	return saved, nil
}

func (s *Subscriptions) ChangePlan(ctx context.Context, id int64, plan domain.Plan) (domain.Subscription, error) {
	subscription, err := s.repository.ByID(ctx, id)
	if err != nil {
		return domain.Subscription{}, err
	}

	previous := subscription.Plan

	// The rule lives on the entity. The use case just applies it and persists.
	if err := subscription.ChangePlan(plan); err != nil {
		return domain.Subscription{}, err
	}

	saved, err := s.repository.Save(ctx, subscription)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("change plan of %d: %w", id, err)
	}

	s.publish(ctx, domain.Event{
		Name:           "subscription.plan_changed",
		SubscriptionID: saved.ID,
		CustomerID:     saved.CustomerID,
		Detail:         fmt.Sprintf("%s -> %s", previous, saved.Plan),
	})

	return saved, nil
}

func (s *Subscriptions) Cancel(ctx context.Context, id int64) (domain.Subscription, error) {
	subscription, err := s.repository.ByID(ctx, id)
	if err != nil {
		return domain.Subscription{}, err
	}

	if err := subscription.Cancel(s.clock.Now()); err != nil {
		return domain.Subscription{}, err
	}

	saved, err := s.repository.Save(ctx, subscription)
	if err != nil {
		return domain.Subscription{}, fmt.Errorf("cancel %d: %w", id, err)
	}

	s.publish(ctx, domain.Event{
		Name:           "subscription.canceled",
		SubscriptionID: saved.ID,
		CustomerID:     saved.CustomerID,
	})

	return saved, nil
}

func (s *Subscriptions) Activate(ctx context.Context, id int64) (domain.Subscription, error) {
	subscription, err := s.repository.ByID(ctx, id)
	if err != nil {
		return domain.Subscription{}, err
	}

	if err := subscription.Activate(); err != nil {
		return domain.Subscription{}, err
	}

	return s.repository.Save(ctx, subscription)
}

func (s *Subscriptions) Get(ctx context.Context, id int64) (domain.Subscription, error) {
	return s.repository.ByID(ctx, id)
}

// MonthlyRevenue is an application-level query: it walks entities and asks
// each one a domain question, rather than reimplementing the pricing rules.
func (s *Subscriptions) MonthlyRevenue(ctx context.Context, currency string) (domain.Money, int, error) {
	subscriptions, err := s.repository.List(ctx, "", 1000)
	if err != nil {
		return domain.Money{}, 0, fmt.Errorf("monthly revenue: %w", err)
	}

	total, err := domain.NewMoney(0, currency)
	if err != nil {
		return domain.Money{}, 0, err
	}

	now := s.clock.Now()
	billable := 0

	for _, subscription := range subscriptions {
		charge, err := subscription.MonthlyCharge(currency, now)
		if err != nil {
			return domain.Money{}, 0, fmt.Errorf("monthly revenue: %w", err)
		}

		if charge.Cents == 0 {
			continue
		}

		billable++

		if total, err = total.Add(charge); err != nil {
			return domain.Money{}, 0, fmt.Errorf("monthly revenue: %w", err)
		}
	}

	return total, billable, nil
}

// publish never fails a use case: a subscription that was created but whose
// event could not be delivered is still created. The error is reported, not
// propagated - a decision the application layer is the right place to make.
func (s *Subscriptions) publish(ctx context.Context, event domain.Event) {
	if s.events == nil {
		return
	}

	if err := s.events.Publish(ctx, event); err != nil {
		// No logging package here either: the port owns how it reports.
		_ = err
	}
}
