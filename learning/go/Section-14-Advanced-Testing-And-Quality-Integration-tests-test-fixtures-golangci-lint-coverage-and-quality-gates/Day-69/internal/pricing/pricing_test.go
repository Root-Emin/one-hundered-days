package pricing_test

import (
	"errors"
	"testing"
	"time"

	pricing "example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-69/internal/pricing"
)

/*
These tests cover the critical path - the branches where a mistake charges the
wrong amount - and deliberately leave presentation and dead code uncovered, so
the coverage report has something real to say.

	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	go tool cover -html=coverage.out    # opens a browser view of the gaps
*/

var now = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func TestQuoteBasics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		order    pricing.Order
		expected int64
	}{
		{
			"single unit, no vat",
			pricing.Order{Plan: pricing.PlanBasic, Quantity: 1, Country: "US"},
			1000,
		},
		{
			"vat applied",
			pricing.Order{Plan: pricing.PlanBasic, Quantity: 1, Country: "TR"},
			1200,
		},
		{
			"unknown country falls back to 20%",
			pricing.Order{Plan: pricing.PlanBasic, Quantity: 1, Country: "ZZ"},
			1200,
		},
		{
			"ten units get 10% off",
			pricing.Order{Plan: pricing.PlanBasic, Quantity: 10, Country: "US"},
			9000,
		},
		{
			"a hundred units get 20% off",
			pricing.Order{Plan: pricing.PlanBasic, Quantity: 100, Country: "US"},
			80000,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			total, err := pricing.Quote(test.order, now)
			if err != nil {
				t.Fatalf("quote: %v", err)
			}

			if total != test.expected {
				t.Fatalf("total = %d, want %d", total, test.expected)
			}
		})
	}
}

func TestQuoteRejectsBadInput(t *testing.T) {
	t.Parallel()

	if _, err := pricing.Quote(pricing.Order{Plan: pricing.PlanBasic, Quantity: 0}, now); !errors.Is(err, pricing.ErrInvalidQuantity) {
		t.Fatalf("err = %v, want ErrInvalidQuantity", err)
	}

	if _, err := pricing.Quote(pricing.Order{Plan: "platinum", Quantity: 1}, now); !errors.Is(err, pricing.ErrUnknownPlan) {
		t.Fatalf("err = %v, want ErrUnknownPlan", err)
	}
}

func TestCoupons(t *testing.T) {
	t.Parallel()

	base := pricing.Order{Plan: pricing.PlanPro, Quantity: 1, Country: "US"}

	t.Run("percentage off", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "SAVE20", PercentOff: 20}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		if total != 2000 {
			t.Fatalf("total = %d, want 2000", total)
		}
	})

	t.Run("discount is capped at 50%", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "TOOMUCH", PercentOff: 95}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		if total != 1250 {
			t.Fatalf("total = %d, want 1250 - the cap did not hold", total)
		}
	})

	t.Run("expired coupon", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "OLD", PercentOff: 20, ExpiresAt: now.Add(-time.Hour)}

		if _, err := pricing.Quote(order, now); !errors.Is(err, pricing.ErrExpiredCoupon) {
			t.Fatalf("err = %v, want ErrExpiredCoupon", err)
		}
	})

	t.Run("first order only, on a repeat order", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "WELCOME", PercentOff: 20, FirstOrderOnly: true}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		if total != 2500 {
			t.Fatalf("total = %d, want the undiscounted 2500", total)
		}
	})
}

// Deliberately NOT tested here:
//
//	FormatCents  - presentation; a wrong string is visible, not expensive
//	LegacyQuote  - dead code; the fix is to delete it, not to test it
//	negative PercentOff, MinimumCents - real gaps, listed in the day's output
//
// The coverage report names all of them. Deciding which ones matter is the
// judgement the number cannot make for you.

//
// Added after reading the coverage report.
//
// These are the uncovered branches that could actually charge a customer the
// wrong amount. FormatCents is included because it is a public function with a
// sign bug waiting to happen; LegacyQuote was deleted instead of tested.
//

func TestCouponEdgeCases(t *testing.T) {
	t.Parallel()

	base := pricing.Order{Plan: pricing.PlanPro, Quantity: 1, Country: "US"}

	t.Run("negative percent is treated as zero", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "BROKEN", PercentOff: -50}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		// A negative discount must not *increase* the price.
		if total != 2500 {
			t.Fatalf("total = %d, want 2500", total)
		}
	})

	t.Run("below the minimum spend the coupon does not apply", func(t *testing.T) {
		order := base
		order.Coupon = &pricing.Coupon{Code: "BIGSPEND", PercentOff: 20, MinimumCents: 10_000}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		if total != 2500 {
			t.Fatalf("total = %d, want the undiscounted 2500", total)
		}
	})

	t.Run("first order coupon on a first order", func(t *testing.T) {
		order := base
		order.FirstOrder = true
		order.Coupon = &pricing.Coupon{Code: "WELCOME", PercentOff: 20, FirstOrderOnly: true}

		total, err := pricing.Quote(order, now)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}

		if total != 2000 {
			t.Fatalf("total = %d, want 2000", total)
		}
	})
}

func TestFormatCents(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		0:      "0.00",
		5:      "0.05",
		12345:  "123.45",
		-12345: "-123.45",
	}

	for cents, want := range tests {
		if got := pricing.FormatCents(cents); got != want {
			t.Errorf("FormatCents(%d) = %q, want %q", cents, got, want)
		}
	}
}
