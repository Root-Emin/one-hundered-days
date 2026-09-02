package main

import (
	"fmt"
	"strings"
	"time"
)

/*
Rich domain types.

The rule followed here is "parse, don't validate": a value of type Email has
already been checked, so no function downstream needs to re-check it, and no
caller can construct one that is wrong. Compare with an anemic model, where
every field is a string and every function re-validates - or forgets to.

Each constructor is the single gate; each type's methods are the only way to
change it. There are no exported mutable fields on the entity below, which is
what makes the invariants actually hold.
*/

//
// VALUE OBJECTS
//

// Email cannot exist in an invalid state. Its zero value is unusable, and the
// only way to get one is through NewEmail.
type Email struct {
	value string
}

func NewEmail(raw string) (Email, error) {
	value := strings.ToLower(strings.TrimSpace(raw))

	switch {
	case value == "":
		return Email{}, invalid("email", "required", "email is required")

	case len(value) > 254:
		return Email{}, invalid("email", "too_long", "email must be at most 254 characters")

	case strings.Count(value, "@") != 1 ||
		strings.HasPrefix(value, "@") || strings.HasSuffix(value, "@") ||
		!strings.Contains(value[strings.Index(value, "@"):], "."):
		return Email{}, invalid("email", "format", "email %q is not a valid address", raw)
	}

	return Email{value: value}, nil
}

func (e Email) String() string { return e.value }

func (e Email) IsZero() bool { return e.value == "" }

// SKU is a domain identifier, not a string. Two SKUs can be compared; a SKU
// and a customer id cannot be confused at a call site.
type SKU struct {
	value string
}

func NewSKU(raw string) (SKU, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))

	if value == "" {
		return SKU{}, invalid("sku", "required", "sku is required")
	}

	if len(value) > 32 {
		return SKU{}, invalid("sku", "too_long", "sku must be at most 32 characters")
	}

	for _, char := range value {
		if !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' {
			return SKU{}, invalid("sku", "format", "sku may contain only A-Z, 0-9 and -")
		}
	}

	return SKU{value: value}, nil
}

func (s SKU) String() string { return s.value }

// Quantity is a positive integer with a ceiling. "quantity int" would let -3
// and 10_000_000 into the system and rely on every caller to check.
type Quantity struct {
	value int
}

const maxQuantity = 1000

func NewQuantity(value int) (Quantity, error) {
	switch {
	case value <= 0:
		return Quantity{}, invalid("quantity", "positive", "quantity must be greater than zero")
	case value > maxQuantity:
		return Quantity{}, invalid("quantity", "max", "quantity must be at most %d", maxQuantity)
	}

	return Quantity{value: value}, nil
}

func (q Quantity) Int() int { return q.value }

func (q Quantity) Add(other Quantity) (Quantity, error) {
	return NewQuantity(q.value + other.value)
}

// Money carries its currency, so adding EUR to USD is an error rather than a
// wrong number.
type Money struct {
	cents    int64
	currency string
}

func NewMoney(cents int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))

	switch {
	case cents < 0:
		return Money{}, invalid("amount", "positive", "amount cannot be negative")
	case len(currency) != 3:
		return Money{}, invalid("currency", "format", "currency must be a 3 letter code")
	}

	return Money{cents: cents, currency: currency}, nil
}

func (m Money) Cents() int64 { return m.cents }

func (m Money) Currency() string { return m.currency }

func (m Money) IsZero() bool { return m.cents == 0 && m.currency == "" }

func (m Money) Add(other Money) (Money, error) {
	if m.IsZero() {
		return other, nil
	}

	if m.currency != other.currency {
		return Money{}, invalid("currency", "mismatch",
			"cannot add %s to %s", other.currency, m.currency)
	}

	return Money{cents: m.cents + other.cents, currency: m.currency}, nil
}

func (m Money) Times(count int) Money {
	return Money{cents: m.cents * int64(count), currency: m.currency}
}

func (m Money) String() string {
	if m.IsZero() {
		return "0.00"
	}

	return fmt.Sprintf("%d.%02d %s", m.cents/100, m.cents%100, m.currency)
}

//
// ENTITY
//

type OrderState string

const (
	OrderDraft     OrderState = "draft"
	OrderSubmitted OrderState = "submitted"
	OrderPaid      OrderState = "paid"
	OrderCancelled OrderState = "cancelled"
)

type OrderLine struct {
	SKU       SKU
	Quantity  Quantity
	UnitPrice Money
}

func (l OrderLine) Total() Money {
	return l.UnitPrice.Times(l.Quantity.Int())
}

// Order keeps its fields unexported: the only way to change an order is
// through a method that enforces the rules. An anemic version with public
// fields would let any caller set State = "paid" and skip the whole workflow.
type Order struct {
	id        int64
	customer  Email
	state     OrderState
	lines     []OrderLine
	createdAt time.Time
	paidAt    time.Time
}

func NewOrder(customer Email, now time.Time) (*Order, error) {
	if customer.IsZero() {
		return nil, invalid("customer_email", "required", "customer email is required")
	}

	return &Order{
		customer:  customer,
		state:     OrderDraft,
		createdAt: now,
	}, nil
}

func (o *Order) ID() int64            { return o.id }
func (o *Order) Customer() Email      { return o.customer }
func (o *Order) State() OrderState    { return o.state }
func (o *Order) CreatedAt() time.Time { return o.createdAt }

// Lines returns a copy: handing out the slice would let a caller append to the
// order without going through AddLine, and the invariants would be gone.
func (o *Order) Lines() []OrderLine {
	return append([]OrderLine(nil), o.lines...)
}

const maxOrderLines = 20

// AddLine is where the invariants live: only a draft can change, a repeated
// SKU merges instead of duplicating, and the currency must stay consistent.
func (o *Order) AddLine(sku SKU, quantity Quantity, unitPrice Money) error {
	if o.state != OrderDraft {
		return StateError{Entity: "order", State: string(o.state), Action: "add a line to",
			Because: "only a draft order can be changed"}
	}

	if !o.currency().IsZero() && o.currency().Currency() != unitPrice.Currency() {
		return invalid("currency", "mismatch",
			"order is in %s, line is in %s", o.currency().Currency(), unitPrice.Currency())
	}

	for i, line := range o.lines {
		if line.SKU == sku {
			merged, err := line.Quantity.Add(quantity)
			if err != nil {
				return err
			}

			o.lines[i].Quantity = merged

			return nil
		}
	}

	if len(o.lines) >= maxOrderLines {
		return invalid("lines", "max", "an order may contain at most %d distinct products", maxOrderLines)
	}

	o.lines = append(o.lines, OrderLine{SKU: sku, Quantity: quantity, UnitPrice: unitPrice})

	return nil
}

func (o *Order) RemoveLine(sku SKU) error {
	if o.state != OrderDraft {
		return StateError{Entity: "order", State: string(o.state), Action: "remove a line from",
			Because: "only a draft order can be changed"}
	}

	for i, line := range o.lines {
		if line.SKU == sku {
			o.lines = append(o.lines[:i], o.lines[i+1:]...)

			return nil
		}
	}

	return NotFoundError{Resource: "order line", ID: sku.String()}
}

// Total is computed, never stored. A stored total is a number that can drift
// out of sync with the lines it claims to summarise.
func (o *Order) Total() Money {
	total := Money{}

	for _, line := range o.lines {
		sum, err := total.Add(line.Total())
		if err != nil {
			// Unreachable: AddLine already enforces one currency per order.
			continue
		}

		total = sum
	}

	return total
}

func (o *Order) currency() Money {
	if len(o.lines) == 0 {
		return Money{}
	}

	return o.lines[0].UnitPrice
}

func (o *Order) Submit() error {
	if o.state != OrderDraft {
		return StateError{Entity: "order", State: string(o.state), Action: "submit"}
	}

	if len(o.lines) == 0 {
		return invalid("lines", "required", "an order needs at least one line before it is submitted")
	}

	o.state = OrderSubmitted

	return nil
}

func (o *Order) MarkPaid(now time.Time) error {
	if o.state != OrderSubmitted {
		return StateError{Entity: "order", State: string(o.state), Action: "pay",
			Because: "only a submitted order can be paid"}
	}

	o.state = OrderPaid
	o.paidAt = now

	return nil
}

func (o *Order) Cancel() error {
	switch o.state {
	case OrderPaid:
		return StateError{Entity: "order", State: string(o.state), Action: "cancel",
			Because: "a paid order must be refunded instead"}

	case OrderCancelled:
		return StateError{Entity: "order", State: string(o.state), Action: "cancel"}
	}

	o.state = OrderCancelled

	return nil
}

func (o *Order) PaidAt() time.Time { return o.paidAt }
