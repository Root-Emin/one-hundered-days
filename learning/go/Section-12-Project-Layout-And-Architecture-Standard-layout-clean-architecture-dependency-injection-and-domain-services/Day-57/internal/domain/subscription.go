// Package domain is the innermost layer: entities, their invariants, the
// errors they raise, and the ports (interfaces) the outer layers must satisfy.
//
// The dependency rule in one sentence: this package imports the standard
// library and nothing else. internal/arch/arch_test.go enforces it, so the
// rule is checked by the build rather than remembered by reviewers.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalid       = errors.New("invalid")
	ErrAlreadyExists = errors.New("already exists")
	ErrNotAllowed    = errors.New("not allowed in this state")
)

//
// VALUE OBJECTS
//

// Money is minor units plus a currency. Making it a type rather than an int64
// means "add 5 EUR to 5 USD" is a compile-time or runtime error instead of a
// silent wrong number.
type Money struct {
	Cents    int64
	Currency string
}

func NewMoney(cents int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))

	if cents < 0 {
		return Money{}, fmt.Errorf("%w: amount cannot be negative", ErrInvalid)
	}

	if len(currency) != 3 {
		return Money{}, fmt.Errorf("%w: currency must be a 3 letter code", ErrInvalid)
	}

	return Money{Cents: cents, Currency: currency}, nil
}

func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: cannot add %s to %s", ErrInvalid, other.Currency, m.Currency)
	}

	return Money{Cents: m.Cents + other.Cents, Currency: m.Currency}, nil
}

func (m Money) String() string {
	return fmt.Sprintf("%d.%02d %s", m.Cents/100, m.Cents%100, m.Currency)
}

//
// ENTITY
//

type Plan string

const (
	PlanFree  Plan = "free"
	PlanPro   Plan = "pro"
	PlanScale Plan = "scale"
)

var planPrices = map[Plan]int64{
	PlanFree:  0,
	PlanPro:   2900,
	PlanScale: 9900,
}

func (p Plan) Valid() bool {
	_, known := planPrices[p]

	return known
}

func (p Plan) MonthlyPrice(currency string) (Money, error) {
	price, known := planPrices[p]
	if !known {
		return Money{}, fmt.Errorf("%w: unknown plan %q", ErrInvalid, p)
	}

	return NewMoney(price, currency)
}

type SubscriptionState string

const (
	StateTrialing SubscriptionState = "trialing"
	StateActive   SubscriptionState = "active"
	StatePastDue  SubscriptionState = "past_due"
	StateCanceled SubscriptionState = "canceled"
)

// Subscription is a rich entity: the state machine lives on the type, so no
// caller can move a subscription into a state the business does not allow.
type Subscription struct {
	ID         int64
	CustomerID string
	Plan       Plan
	State      SubscriptionState
	StartedAt  time.Time
	TrialEnds  time.Time
	CanceledAt time.Time
}

func NewSubscription(customerID string, plan Plan, now time.Time, trial time.Duration) (Subscription, error) {
	customerID = strings.TrimSpace(customerID)

	switch {
	case customerID == "":
		return Subscription{}, fmt.Errorf("%w: customer id is required", ErrInvalid)

	case !plan.Valid():
		return Subscription{}, fmt.Errorf("%w: plan %q does not exist", ErrInvalid, plan)

	case trial < 0:
		return Subscription{}, fmt.Errorf("%w: trial cannot be negative", ErrInvalid)
	}

	subscription := Subscription{
		CustomerID: customerID,
		Plan:       plan,
		State:      StateActive,
		StartedAt:  now,
	}

	if trial > 0 {
		subscription.State = StateTrialing
		subscription.TrialEnds = now.Add(trial)
	}

	return subscription, nil
}

// Activate ends a trial. Every transition is a method, so the set of legal
// moves is readable in one place.
func (s *Subscription) Activate() error {
	if s.State != StateTrialing && s.State != StatePastDue {
		return fmt.Errorf("%w: cannot activate a %s subscription", ErrNotAllowed, s.State)
	}

	s.State = StateActive
	s.TrialEnds = time.Time{}

	return nil
}

func (s *Subscription) MarkPastDue() error {
	if s.State != StateActive {
		return fmt.Errorf("%w: only an active subscription can fall past due", ErrNotAllowed)
	}

	s.State = StatePastDue

	return nil
}

func (s *Subscription) Cancel(now time.Time) error {
	if s.State == StateCanceled {
		return fmt.Errorf("%w: already canceled", ErrNotAllowed)
	}

	s.State = StateCanceled
	s.CanceledAt = now

	return nil
}

// ChangePlan carries the rule that a canceled subscription is immutable and
// that a downgrade out of a trial ends the trial.
func (s *Subscription) ChangePlan(plan Plan) error {
	if !plan.Valid() {
		return fmt.Errorf("%w: plan %q does not exist", ErrInvalid, plan)
	}

	if s.State == StateCanceled {
		return fmt.Errorf("%w: a canceled subscription cannot change plan", ErrNotAllowed)
	}

	if plan == s.Plan {
		return fmt.Errorf("%w: already on the %s plan", ErrInvalid, plan)
	}

	if plan == PlanFree && s.State == StateTrialing {
		s.State = StateActive
		s.TrialEnds = time.Time{}
	}

	s.Plan = plan

	return nil
}

func (s Subscription) Billable(now time.Time) bool {
	if s.State != StateActive && s.State != StatePastDue {
		return false
	}

	return s.Plan != PlanFree && (s.TrialEnds.IsZero() || now.After(s.TrialEnds))
}

func (s Subscription) MonthlyCharge(currency string, now time.Time) (Money, error) {
	if !s.Billable(now) {
		return NewMoney(0, currency)
	}

	return s.Plan.MonthlyPrice(currency)
}
