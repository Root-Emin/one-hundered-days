package domain

import (
	"errors"
	"testing"
	"time"
)

// The domain is testable with nothing but the standard library: no server, no
// database, no fixtures. That is the payoff of the dependency rule.

var reference = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func TestNewSubscription(t *testing.T) {
	t.Parallel()

	subscription, err := NewSubscription("cus_1", PlanPro, reference, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if subscription.State != StateTrialing {
		t.Fatalf("state = %q, want trialing", subscription.State)
	}

	if !subscription.TrialEnds.Equal(reference.Add(14 * 24 * time.Hour)) {
		t.Fatalf("trial ends = %s", subscription.TrialEnds)
	}

	withoutTrial, err := NewSubscription("cus_2", PlanFree, reference, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if withoutTrial.State != StateActive {
		t.Fatalf("state = %q, want active", withoutTrial.State)
	}
}

func TestNewSubscriptionRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		customer string
		plan     Plan
		trial    time.Duration
	}{
		{"empty customer", "  ", PlanPro, 0},
		{"unknown plan", "cus_1", Plan("platinum"), 0},
		{"negative trial", "cus_1", PlanPro, -time.Hour},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSubscription(test.customer, test.plan, reference, test.trial); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestStateMachine(t *testing.T) {
	t.Parallel()

	subscription, err := NewSubscription("cus_1", PlanPro, reference, time.Hour)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := subscription.Activate(); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if subscription.State != StateActive || !subscription.TrialEnds.IsZero() {
		t.Fatalf("subscription = %+v", subscription)
	}

	if err := subscription.Activate(); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("double activate err = %v, want ErrNotAllowed", err)
	}

	if err := subscription.MarkPastDue(); err != nil {
		t.Fatalf("mark past due: %v", err)
	}

	if err := subscription.Activate(); err != nil {
		t.Fatalf("recover from past due: %v", err)
	}

	if err := subscription.Cancel(reference); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if err := subscription.Cancel(reference); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("double cancel err = %v, want ErrNotAllowed", err)
	}

	if err := subscription.ChangePlan(PlanScale); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("plan change after cancel err = %v, want ErrNotAllowed", err)
	}
}

func TestChangePlan(t *testing.T) {
	t.Parallel()

	subscription, err := NewSubscription("cus_1", PlanPro, reference, time.Hour)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := subscription.ChangePlan(PlanPro); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same plan err = %v, want ErrInvalid", err)
	}

	// Downgrading to free during a trial ends the trial: the rule is on the
	// entity, so no caller can forget it.
	if err := subscription.ChangePlan(PlanFree); err != nil {
		t.Fatalf("downgrade: %v", err)
	}

	if subscription.State != StateActive || !subscription.TrialEnds.IsZero() {
		t.Fatalf("subscription = %+v, want an active non-trial subscription", subscription)
	}
}

func TestBillableAndCharge(t *testing.T) {
	t.Parallel()

	trialing, err := NewSubscription("cus_1", PlanPro, reference, 24*time.Hour)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if trialing.Billable(reference) {
		t.Fatal("a trialing subscription is billable")
	}

	// Time passing is not enough: a trial becomes billable when it is
	// converted, which is an explicit transition rather than a side effect of
	// the clock. That distinction is why Activate exists.
	if trialing.Billable(reference.Add(48 * time.Hour)) {
		t.Fatal("an unconverted trial became billable just because time passed")
	}

	converted := trialing

	if err := converted.Activate(); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if !converted.Billable(reference.Add(48 * time.Hour)) {
		t.Fatal("a converted trial is not billable")
	}

	charge, err := trialing.MonthlyCharge("EUR", reference)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}

	if charge.Cents != 0 {
		t.Fatalf("charge during trial = %s, want zero", charge)
	}

	charge, err = converted.MonthlyCharge("EUR", reference.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("charge: %v", err)
	}

	if charge.Cents != 2900 || charge.Currency != "EUR" {
		t.Fatalf("charge after conversion = %s, want 29.00 EUR", charge)
	}

	free, err := NewSubscription("cus_2", PlanFree, reference, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if free.Billable(reference) {
		t.Fatal("the free plan is billable")
	}
}

func TestMoney(t *testing.T) {
	t.Parallel()

	first, err := NewMoney(2900, "eur")
	if err != nil {
		t.Fatalf("new money: %v", err)
	}

	if first.Currency != "EUR" || first.String() != "29.00 EUR" {
		t.Fatalf("money = %+v (%s)", first, first)
	}

	second, err := NewMoney(100, "EUR")
	if err != nil {
		t.Fatalf("new money: %v", err)
	}

	total, err := first.Add(second)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if total.Cents != 3000 {
		t.Fatalf("total = %s", total)
	}

	// Mixing currencies is a bug the type system surfaces instead of hiding.
	usd, err := NewMoney(100, "USD")
	if err != nil {
		t.Fatalf("new money: %v", err)
	}

	if _, err := first.Add(usd); !errors.Is(err, ErrInvalid) {
		t.Fatalf("adding USD to EUR err = %v, want ErrInvalid", err)
	}

	if _, err := NewMoney(-1, "EUR"); !errors.Is(err, ErrInvalid) {
		t.Fatal("a negative amount was accepted")
	}

	if _, err := NewMoney(100, "EURO"); !errors.Is(err, ErrInvalid) {
		t.Fatal("a four letter currency was accepted")
	}
}
