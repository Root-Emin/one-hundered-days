package perf

import (
	"errors"
	"fmt"
	"time"
)

// Budget is a performance regression gate.
//
// The purpose is not to pin the numbers - they move with the machine, the Go
// version and the weather. It is to catch the change that puts an N+1 back, or
// drops the index in a migration, or reintroduces a per-row allocation. Those
// show up as an order of magnitude, not as five percent.
//
// So the thresholds are set generously, well above the measured value. A gate
// that fails on noise gets disabled within a month, and then it protects
// nothing.
type Budget struct {
	Name string

	// MaxP95 fails the build when the 95th percentile regresses past it.
	MaxP95 time.Duration

	// MaxQueriesPerRequest catches the N+1 coming back. This is the strongest
	// guard on the page, because the query count is exact - it does not vary
	// with load, machine or scheduler.
	MaxQueriesPerRequest float64

	// MaxAllocsPerOp catches an allocation regression in a benchmark.
	MaxAllocsPerOp int64

	// MinThroughput fails when requests per second fall below this.
	MinThroughput float64
}

// ErrBudgetExceeded is returned by Check.
var ErrBudgetExceeded = errors.New("performance budget exceeded")

// Check compares a measurement against the budget and returns every violation.
func (b Budget) Check(result Result, queriesPerRequest float64, allocsPerOp int64) error {
	var violations []error

	if b.MaxP95 > 0 && result.P95 > b.MaxP95 {
		violations = append(violations, fmt.Errorf("p95 %s exceeds %s",
			result.P95.Round(time.Microsecond), b.MaxP95))
	}

	if b.MaxQueriesPerRequest > 0 && queriesPerRequest > b.MaxQueriesPerRequest {
		violations = append(violations, fmt.Errorf("%.1f queries per request exceeds %.1f",
			queriesPerRequest, b.MaxQueriesPerRequest))
	}

	if b.MaxAllocsPerOp > 0 && allocsPerOp > b.MaxAllocsPerOp {
		violations = append(violations, fmt.Errorf("%d allocs/op exceeds %d",
			allocsPerOp, b.MaxAllocsPerOp))
	}

	if b.MinThroughput > 0 && result.Throughput < b.MinThroughput {
		violations = append(violations, fmt.Errorf("%.0f req/s is below %.0f",
			result.Throughput, b.MinThroughput))
	}

	if len(violations) == 0 {
		return nil
	}

	return fmt.Errorf("%s: %w: %w", b.Name, ErrBudgetExceeded, errors.Join(violations...))
}
