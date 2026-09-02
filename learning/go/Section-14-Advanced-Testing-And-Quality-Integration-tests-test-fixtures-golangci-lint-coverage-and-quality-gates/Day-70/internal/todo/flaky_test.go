package todo_test

import (
	"context"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/testsupport"
	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/todo"
)

/*
The flaky test that started this, and the fix.

What happened: List sorted only by CreatedAt. Two tasks created in the same
clock tick compared equal, so their relative order came from Go's randomised
map iteration - the assertion passed locally and failed in CI roughly one run
in three.

Three ways to react, in order of preference:

 1. Fix the code. The order was genuinely undefined; List now breaks ties by
    ID, and TestListOrderIsDeterministic below proves it.
 2. Fix the test, when the code is right and the assertion was too strict.
 3. Quarantine it - move it behind a build tag with an issue number, so it
    stops eroding trust in the suite while somebody works on it. That is what
    the 'flaky' tag on quarantine_test.go is for.

What NOT to do: add a retry, add a sleep, or delete the assertion. All three
hide the bug and keep the failure in production.

Hunt for flakes with:

	go test -count=20 -race ./...
	go test -count=20 -shuffle=on ./...
*/

// TestListOrderIsDeterministic runs the previously flaky assertion many times.
// With the tie-break in place it cannot fail; before the fix, -count=50 failed
// almost every time.
func TestListOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	for attempt := range 50 {
		service, _ := testsupport.NewService(t)
		ctx := context.Background()

		// All three are created within the same fixed instant, which is
		// exactly the condition that made the order ambiguous.
		var ids []int64

		for _, title := range []string{"first", "second", "third"} {
			task, err := service.Create(ctx, "ada", title, time.Time{})
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			ids = append(ids, task.ID)
		}

		listed, err := service.List(ctx, "ada", false)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		for i, task := range listed {
			if task.ID != ids[i] {
				t.Fatalf("attempt %d: order = %v, want %v - the tie-break is gone",
					attempt, idsOf(listed), ids)
			}
		}
	}
}

func idsOf(tasks []todo.Task) []int64 {
	ids := make([]int64, 0, len(tasks))

	for _, task := range tasks {
		ids = append(ids, task.ID)
	}

	return ids
}
