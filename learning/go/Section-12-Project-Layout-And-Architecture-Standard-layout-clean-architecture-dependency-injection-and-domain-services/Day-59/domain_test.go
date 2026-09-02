package main

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func mustEmail(t *testing.T, value string) Email {
	t.Helper()

	email, err := NewEmail(value)
	if err != nil {
		t.Fatalf("email %q: %v", value, err)
	}

	return email
}

func mustSKU(t *testing.T, value string) SKU {
	t.Helper()

	sku, err := NewSKU(value)
	if err != nil {
		t.Fatalf("sku %q: %v", value, err)
	}

	return sku
}

func mustQuantity(t *testing.T, value int) Quantity {
	t.Helper()

	quantity, err := NewQuantity(value)
	if err != nil {
		t.Fatalf("quantity %d: %v", value, err)
	}

	return quantity
}

func mustMoney(t *testing.T, cents int64, currency string) Money {
	t.Helper()

	money, err := NewMoney(cents, currency)
	if err != nil {
		t.Fatalf("money: %v", err)
	}

	return money
}

//
// VALUE OBJECTS
//

func TestValueObjectsRejectInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() error
		field string
	}{
		{"empty email", func() error { _, err := NewEmail(""); return err }, "email"},
		{"email without @", func() error { _, err := NewEmail("ada.example.com"); return err }, "email"},
		{"email with two @", func() error { _, err := NewEmail("a@b@c.com"); return err }, "email"},
		{"email without dot", func() error { _, err := NewEmail("ada@localhost"); return err }, "email"},
		{"empty sku", func() error { _, err := NewSKU(" "); return err }, "sku"},
		{"sku with a space", func() error { _, err := NewSKU("KB 01"); return err }, "sku"},
		{"quantity zero", func() error { _, err := NewQuantity(0); return err }, "quantity"},
		{"quantity negative", func() error { _, err := NewQuantity(-1); return err }, "quantity"},
		{"quantity too large", func() error { _, err := NewQuantity(1001); return err }, "quantity"},
		{"negative money", func() error { _, err := NewMoney(-1, "EUR"); return err }, "amount"},
		{"bad currency", func() error { _, err := NewMoney(1, "EURO"); return err }, "currency"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.build()

			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}

			var fieldError FieldError

			if !errors.As(err, &fieldError) {
				t.Fatalf("err = %v, want a FieldError", err)
			}

			if fieldError.Field != test.field {
				t.Fatalf("field = %q, want %q", fieldError.Field, test.field)
			}
		})
	}
}

func TestValueObjectsNormalise(t *testing.T) {
	t.Parallel()

	if email := mustEmail(t, "  ADA@Example.COM "); email.String() != "ada@example.com" {
		t.Fatalf("email = %q", email)
	}

	if sku := mustSKU(t, " kb-01 "); sku.String() != "KB-01" {
		t.Fatalf("sku = %q", sku)
	}

	if money := mustMoney(t, 12900, "eur"); money.Currency() != "EUR" {
		t.Fatalf("currency = %q", money.Currency())
	}
}

func TestMoneyArithmetic(t *testing.T) {
	t.Parallel()

	first := mustMoney(t, 12900, "EUR")
	second := mustMoney(t, 4900, "EUR")

	total, err := first.Add(second)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if total.Cents() != 17800 {
		t.Fatalf("total = %s", total)
	}

	if doubled := first.Times(2); doubled.Cents() != 25800 {
		t.Fatalf("times = %s", doubled)
	}

	// The type prevents an entire class of financial bug.
	if _, err := first.Add(mustMoney(t, 100, "USD")); !errors.Is(err, ErrValidation) {
		t.Fatal("EUR + USD was allowed")
	}
}

//
// ENTITY INVARIANTS
//

func newDraft(t *testing.T) *Order {
	t.Helper()

	order, err := NewOrder(mustEmail(t, "ada@example.com"), now)
	if err != nil {
		t.Fatalf("new order: %v", err)
	}

	return order
}

func TestOrderMergesRepeatedSKUs(t *testing.T) {
	t.Parallel()

	order := newDraft(t)
	price := mustMoney(t, 12900, "EUR")

	for range 3 {
		if err := order.AddLine(mustSKU(t, "kb-01"), mustQuantity(t, 1), price); err != nil {
			t.Fatalf("add line: %v", err)
		}
	}

	lines := order.Lines()

	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1 merged line", len(lines))
	}

	if lines[0].Quantity.Int() != 3 {
		t.Fatalf("quantity = %d, want 3", lines[0].Quantity.Int())
	}

	if order.Total().Cents() != 38700 {
		t.Fatalf("total = %s, want 387.00 EUR", order.Total())
	}
}

func TestOrderRejectsMixedCurrencies(t *testing.T) {
	t.Parallel()

	order := newDraft(t)

	if err := order.AddLine(mustSKU(t, "kb-01"), mustQuantity(t, 1), mustMoney(t, 12900, "EUR")); err != nil {
		t.Fatalf("add line: %v", err)
	}

	err := order.AddLine(mustSKU(t, "ms-02"), mustQuantity(t, 1), mustMoney(t, 4900, "USD"))

	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// TestLinesAreACopy guards the invariant against the sneakiest bug: a caller
// holding the internal slice and appending to it.
func TestLinesAreACopy(t *testing.T) {
	t.Parallel()

	order := newDraft(t)

	if err := order.AddLine(mustSKU(t, "kb-01"), mustQuantity(t, 1), mustMoney(t, 12900, "EUR")); err != nil {
		t.Fatalf("add line: %v", err)
	}

	lines := order.Lines()
	lines[0].Quantity = mustQuantity(t, 999)
	lines = append(lines, OrderLine{})

	if order.Lines()[0].Quantity.Int() != 1 || len(order.Lines()) != 1 {
		t.Fatal("the caller mutated the order through the slice returned by Lines()")
	}
}

func TestOrderStateMachine(t *testing.T) {
	t.Parallel()

	order := newDraft(t)

	// An empty order cannot be submitted.
	if err := order.Submit(); !errors.Is(err, ErrValidation) {
		t.Fatalf("submitting an empty order err = %v, want ErrValidation", err)
	}

	if err := order.AddLine(mustSKU(t, "kb-01"), mustQuantity(t, 1), mustMoney(t, 12900, "EUR")); err != nil {
		t.Fatalf("add line: %v", err)
	}

	// Paying before submitting is a state error.
	if err := order.MarkPaid(now); !errors.Is(err, ErrConflict) {
		t.Fatalf("paying a draft err = %v, want ErrConflict", err)
	}

	if err := order.Submit(); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// A submitted order is frozen.
	if err := order.AddLine(mustSKU(t, "ms-02"), mustQuantity(t, 1), mustMoney(t, 4900, "EUR")); !errors.Is(err, ErrConflict) {
		t.Fatalf("adding to a submitted order err = %v, want ErrConflict", err)
	}

	if err := order.RemoveLine(mustSKU(t, "kb-01")); !errors.Is(err, ErrConflict) {
		t.Fatalf("removing from a submitted order err = %v, want ErrConflict", err)
	}

	if err := order.MarkPaid(now); err != nil {
		t.Fatalf("pay: %v", err)
	}

	// A paid order cannot be cancelled, and the error says why.
	err := order.Cancel()

	var stateError StateError

	if !errors.As(err, &stateError) {
		t.Fatalf("err = %v, want a StateError", err)
	}

	if stateError.Because == "" {
		t.Fatal("the state error carries no explanation")
	}
}

func TestRemoveMissingLine(t *testing.T) {
	t.Parallel()

	order := newDraft(t)

	err := order.RemoveLine(mustSKU(t, "zz-99"))

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	var notFound NotFoundError

	if !errors.As(err, &notFound) || notFound.ID != "ZZ-99" {
		t.Fatalf("err = %v, want a NotFoundError naming the sku", err)
	}
}

func TestTotalIsAlwaysConsistent(t *testing.T) {
	t.Parallel()

	order := newDraft(t)

	if err := order.AddLine(mustSKU(t, "kb-01"), mustQuantity(t, 2), mustMoney(t, 12900, "EUR")); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := order.AddLine(mustSKU(t, "ms-02"), mustQuantity(t, 3), mustMoney(t, 4900, "EUR")); err != nil {
		t.Fatalf("add: %v", err)
	}

	if order.Total().Cents() != 2*12900+3*4900 {
		t.Fatalf("total = %s", order.Total())
	}

	// Removing a line updates the total, because the total is derived and
	// never stored.
	if err := order.RemoveLine(mustSKU(t, "ms-02")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if order.Total().Cents() != 2*12900 {
		t.Fatalf("total after removal = %s", order.Total())
	}
}
