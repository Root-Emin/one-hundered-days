// Package pricing is the code under measurement.
//
// It is deliberately uneven: the critical path is well covered by
// pricing_test.go, and a couple of branches are not. Reading the coverage
// report next to this file is the exercise - the goal is to notice which
// uncovered lines matter, not to make the number go up.
package pricing

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidQuantity = errors.New("quantity must be positive")
	ErrUnknownPlan     = errors.New("unknown plan")
	ErrExpiredCoupon   = errors.New("coupon expired")
)

type Plan string

const (
	PlanBasic      Plan = "basic"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
)

var unitPrices = map[Plan]int64{
	PlanBasic:      1000,
	PlanPro:        2500,
	PlanEnterprise: 9000,
}

type Coupon struct {
	Code           string
	PercentOff     int
	ExpiresAt      time.Time
	MinimumCents   int64
	FirstOrderOnly bool
}

type Order struct {
	Plan       Plan
	Quantity   int
	Coupon     *Coupon
	FirstOrder bool
	Country    string
}

// Quote is the critical path: money leaves the building through this function,
// so every branch in it deserves a test.
func Quote(order Order, now time.Time) (int64, error) {
	if order.Quantity <= 0 {
		return 0, fmt.Errorf("%w: got %d", ErrInvalidQuantity, order.Quantity)
	}

	unit, known := unitPrices[order.Plan]
	if !known {
		return 0, fmt.Errorf("%w: %q", ErrUnknownPlan, order.Plan)
	}

	subtotal := unit * int64(order.Quantity)

	// Volume discount, applied before any coupon.
	switch {
	case order.Quantity >= 100:
		subtotal = subtotal * 80 / 100
	case order.Quantity >= 10:
		subtotal = subtotal * 90 / 100
	}

	if order.Coupon != nil {
		discounted, err := applyCoupon(subtotal, *order.Coupon, order.FirstOrder, now)
		if err != nil {
			return 0, err
		}

		subtotal = discounted
	}

	return subtotal + vat(subtotal, order.Country), nil
}

func applyCoupon(subtotal int64, coupon Coupon, firstOrder bool, now time.Time) (int64, error) {
	if !coupon.ExpiresAt.IsZero() && now.After(coupon.ExpiresAt) {
		return 0, fmt.Errorf("%w: %s", ErrExpiredCoupon, coupon.Code)
	}

	if coupon.FirstOrderOnly && !firstOrder {
		// Not an error: the coupon simply does not apply.
		return subtotal, nil
	}

	if subtotal < coupon.MinimumCents {
		return subtotal, nil
	}

	percent := coupon.PercentOff

	if percent < 0 {
		percent = 0
	}

	if percent > 50 {
		// A capped discount is a business rule, and an uncapped one is how a
		// coupon bug becomes a refund project.
		percent = 50
	}

	return subtotal * int64(100-percent) / 100, nil
}

// vat is part of the critical path too: getting it wrong is a tax problem.
func vat(subtotal int64, country string) int64 {
	rates := map[string]int64{
		"TR": 20,
		"DE": 19,
		"IE": 23,
		"US": 0,
	}

	rate, known := rates[strings.ToUpper(strings.TrimSpace(country))]
	if !known {
		rate = 20
	}

	return subtotal * rate / 100
}

// FormatCents is presentation, not money movement. It is uncovered on purpose:
// a coverage report that flags this alongside Quote is telling you the number
// alone cannot rank risk.
func FormatCents(cents int64) string {
	negative := cents < 0

	if negative {
		cents = -cents
	}

	formatted := fmt.Sprintf("%d.%02d", cents/100, cents%100)

	if negative {
		return "-" + formatted
	}

	return formatted
}

// LegacyQuote used to live here. The coverage report showed it at 0%, nothing
// called it, and the right fix for dead code is deletion rather than a test
// written to move a number. That is the second thing coverage is good at.
