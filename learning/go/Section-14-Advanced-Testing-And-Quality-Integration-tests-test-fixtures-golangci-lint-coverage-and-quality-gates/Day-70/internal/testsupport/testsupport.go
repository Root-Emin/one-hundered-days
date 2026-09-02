// Package testsupport centralises the fixtures every test in this module uses.
package testsupport

import (
	"context"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

// FixedClock lets a test decide what "now" is.
type FixedClock struct {
	Current time.Time
}

func (c *FixedClock) Now() time.Time { return c.Current }

func (c *FixedClock) Advance(d time.Duration) { c.Current = c.Current.Add(d) }

// Reference is the instant every test starts from, so assertions about dates
// never depend on the day the suite runs.
var Reference = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

const (
	AdaToken  = "ada-token"
	AlanToken = "alan-token"
)

// NewService returns a service with two known users and a controllable clock.
func NewService(t *testing.T) (*todo.Service, *FixedClock) {
	t.Helper()

	tokens := todo.NewTokenStore()
	tokens.Add(AdaToken, "ada")
	tokens.Add(AlanToken, "alan")

	clock := &FixedClock{Current: Reference}

	return todo.NewService(tokens, clock), clock
}

// SeedTasks inserts a known set of tasks and returns them.
func SeedTasks(t *testing.T, service *todo.Service) []todo.Task {
	t.Helper()

	ctx := context.Background()

	seeds := []struct {
		owner string
		title string
		due   time.Time
	}{
		{"ada", "write the integration tests", Reference.Add(24 * time.Hour)},
		{"ada", "run the linter", time.Time{}},
		{"alan", "not ada's task", time.Time{}},
	}

	created := make([]todo.Task, 0, len(seeds))

	for _, seed := range seeds {
		task, err := service.Create(ctx, seed.owner, seed.title, seed.due)
		if err != nil {
			t.Fatalf("seed %q: %v", seed.title, err)
		}

		created = append(created, task)
	}

	return created
}
