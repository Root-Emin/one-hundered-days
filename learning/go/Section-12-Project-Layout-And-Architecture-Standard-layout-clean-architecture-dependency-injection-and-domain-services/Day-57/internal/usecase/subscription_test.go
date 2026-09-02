package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/domain"
)

/*
Use case tests with fakes.

No HTTP, no database, no sleeping for a trial to expire: the ports are
satisfied by twenty lines of in-memory code and a clock the test controls.
That is what the dependency rule buys - fast, deterministic tests of the
application logic.
*/

type fakeRepository struct {
	mu            sync.Mutex
	subscriptions map[int64]domain.Subscription
	nextID        int64
	saveErr       error // injectable failure, to test the unhappy path
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{subscriptions: map[int64]domain.Subscription{}, nextID: 1}
}

var _ domain.SubscriptionRepository = (*fakeRepository)(nil)

func (r *fakeRepository) Save(ctx context.Context, subscription domain.Subscription) (domain.Subscription, error) {
	if r.saveErr != nil {
		return domain.Subscription{}, r.saveErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if subscription.ID == 0 {
		subscription.ID = r.nextID
		r.nextID++
	}

	r.subscriptions[subscription.ID] = subscription

	return subscription, nil
}

func (r *fakeRepository) ByID(ctx context.Context, id int64) (domain.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	subscription, found := r.subscriptions[id]
	if !found {
		return domain.Subscription{}, domain.ErrNotFound
	}

	return subscription, nil
}

func (r *fakeRepository) ByCustomer(ctx context.Context, customerID string) (domain.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, subscription := range r.subscriptions {
		if subscription.CustomerID == customerID {
			return subscription, nil
		}
	}

	return domain.Subscription{}, domain.ErrNotFound
}

func (r *fakeRepository) List(ctx context.Context, state domain.SubscriptionState, limit int) ([]domain.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	matched := make([]domain.Subscription, 0, len(r.subscriptions))

	for _, subscription := range r.subscriptions {
		if state == "" || subscription.State == state {
			matched = append(matched, subscription)
		}
	}

	return matched, nil
}

type fakePublisher struct {
	mu     sync.Mutex
	events []domain.Event
}

var _ domain.EventPublisher = (*fakePublisher)(nil)

func (p *fakePublisher) Publish(ctx context.Context, event domain.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.events = append(p.events, event)

	return nil
}

func (p *fakePublisher) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	names := make([]string, 0, len(p.events))

	for _, event := range p.events {
		names = append(names, event.Name)
	}

	return names
}

// fixedClock makes every time-dependent rule deterministic.
type fixedClock struct {
	now time.Time
}

var _ domain.Clock = (*fixedClock)(nil)

func (c *fixedClock) Now() time.Time { return c.now }

func (c *fixedClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestSubscriptions(t *testing.T) (*Subscriptions, *fakeRepository, *fakePublisher, *fixedClock) {
	t.Helper()

	repository := newFakeRepository()
	publisher := &fakePublisher{}
	clock := &fixedClock{now: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)}

	return NewSubscriptions(repository, publisher, clock), repository, publisher, clock
}

func TestSubscribe(t *testing.T) {
	t.Parallel()

	subscriptions, _, publisher, clock := newTestSubscriptions(t)
	ctx := context.Background()

	subscription, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, true)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if subscription.ID == 0 || subscription.State != domain.StateTrialing {
		t.Fatalf("subscription = %+v", subscription)
	}

	if !subscription.TrialEnds.Equal(clock.now.Add(defaultTrial)) {
		t.Fatalf("trial ends = %s, want %s", subscription.TrialEnds, clock.now.Add(defaultTrial))
	}

	if events := publisher.names(); len(events) != 1 || events[0] != "subscription.created" {
		t.Fatalf("events = %v", events)
	}
}

func TestSubscribeTwiceIsRejected(t *testing.T) {
	t.Parallel()

	subscriptions, _, _, _ := newTestSubscriptions(t)
	ctx := context.Background()

	if _, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, false); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanScale, false)

	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
}

// A customer who cancelled may subscribe again: the rule is "no *active*
// duplicate", not "never twice".
func TestResubscribeAfterCancel(t *testing.T) {
	t.Parallel()

	subscriptions, _, _, _ := newTestSubscriptions(t)
	ctx := context.Background()

	first, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, false)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := subscriptions.Cancel(ctx, first.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanScale, false); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
}

func TestChangePlanEmitsEvent(t *testing.T) {
	t.Parallel()

	subscriptions, _, publisher, _ := newTestSubscriptions(t)
	ctx := context.Background()

	subscription, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, false)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	changed, err := subscriptions.ChangePlan(ctx, subscription.ID, domain.PlanScale)
	if err != nil {
		t.Fatalf("change plan: %v", err)
	}

	if changed.Plan != domain.PlanScale {
		t.Fatalf("plan = %s", changed.Plan)
	}

	events := publisher.names()

	if len(events) != 2 || events[1] != "subscription.plan_changed" {
		t.Fatalf("events = %v", events)
	}
}

func TestUseCaseSurfacesDomainErrors(t *testing.T) {
	t.Parallel()

	subscriptions, _, _, _ := newTestSubscriptions(t)
	ctx := context.Background()

	if _, err := subscriptions.Get(ctx, 42); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	subscription, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, false)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// An unknown plan is a domain rule, reported as ErrInvalid all the way up.
	if _, err := subscriptions.ChangePlan(ctx, subscription.ID, domain.Plan("platinum")); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}

	if _, err := subscriptions.Cancel(ctx, subscription.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := subscriptions.Cancel(ctx, subscription.ID); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
}

func TestStorageFailurePropagates(t *testing.T) {
	t.Parallel()

	subscriptions, repository, publisher, _ := newTestSubscriptions(t)

	repository.saveErr = errors.New("disk on fire")

	if _, err := subscriptions.Subscribe(context.Background(), "cus_1", domain.PlanPro, false); err == nil {
		t.Fatal("a storage failure was swallowed")
	}

	// And nothing was announced for an action that did not happen.
	if events := publisher.names(); len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}

// TestMonthlyRevenueUsesTheClock is the reason the clock is a port: the same
// data yields a different answer before and after the trials end, and the test
// can visit both moments instantly.
func TestMonthlyRevenueUsesTheClock(t *testing.T) {
	t.Parallel()

	subscriptions, _, _, clock := newTestSubscriptions(t)
	ctx := context.Background()

	if _, err := subscriptions.Subscribe(ctx, "cus_1", domain.PlanPro, true); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := subscriptions.Subscribe(ctx, "cus_2", domain.PlanScale, false); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := subscriptions.Subscribe(ctx, "cus_3", domain.PlanFree, false); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	total, billable, err := subscriptions.MonthlyRevenue(ctx, "EUR")
	if err != nil {
		t.Fatalf("revenue: %v", err)
	}

	// Only cus_2 pays today: cus_1 is in a trial and cus_3 is on free.
	if total.Cents != 9900 || billable != 1 {
		t.Fatalf("revenue = %s (%d billable), want 99.00 EUR from 1", total, billable)
	}

	// Move past the trial without waiting two weeks. Time alone is not
	// enough: the state machine requires the trial to be converted, which is
	// what a nightly billing job would do.
	clock.advance(defaultTrial + time.Hour)

	total, billable, err = subscriptions.MonthlyRevenue(ctx, "EUR")
	if err != nil {
		t.Fatalf("revenue: %v", err)
	}

	if billable != 1 {
		t.Fatalf("an unconverted trial is being billed: %d billable", billable)
	}

	if _, err := subscriptions.Activate(ctx, 1); err != nil {
		t.Fatalf("activate: %v", err)
	}

	total, billable, err = subscriptions.MonthlyRevenue(ctx, "EUR")
	if err != nil {
		t.Fatalf("revenue: %v", err)
	}

	if total.Cents != 12800 || billable != 2 {
		t.Fatalf("revenue after conversion = %s (%d billable), want 128.00 EUR from 2", total, billable)
	}
}
