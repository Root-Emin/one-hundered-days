//go:build flaky

// Quarantined tests.
//
// These are excluded from every normal run and from CI:
//
//	go test ./...                 # does not include this file
//	go test -tags=flaky ./...     # runs it; expect intermittent failures
//	go test -tags=flaky -count=10 ./...   # watch it fail some of the time
//
// A quarantine is a promise, not a graveyard. Each test carries an owner and
// an issue, and the rule on this team is that it may stay here for one sprint.
// After that it is fixed or deleted - a quarantine nobody empties is just a
// slower way of deleting tests.

package todo_test

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-14-Advanced-Testing-And-Quality-Integration-tests-test-fixtures-golangci-lint-coverage-and-quality-gates/Day-70/internal/testsupport"
)

// FLAKY - issue #412 - owner: @ada - quarantined 2026-03-01
//
// Symptom: fails roughly half the time, with no code change in between.
//
// Root cause: the test starts background work and then SLEEPS, hoping the work
// finished. Whether it did depends on the scheduler and on how busy the
// machine is - so the assertion is about the CI runner, not about the code.
//
// The fix (not applied here, on purpose): wait for a signal instead of a
// duration. A channel closed by the worker, or a sync.WaitGroup, turns a
// probabilistic test into a deterministic one:
//
//	done := make(chan struct{})
//	go func() { defer close(done); ... }()
//	<-done
//
// Anything of the form "sleep, then assert" belongs on this list.
func TestBackgroundImportFinishesInTime(t *testing.T) {
	service, _ := testsupport.NewService(t)

	var waitGroup sync.WaitGroup

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		// Stand-in for real background work whose duration varies.
		time.Sleep(time.Duration(rand.IntN(4)) * time.Millisecond)

		for i := range 3 {
			if _, err := service.Create(context.Background(), "ada", "imported task", time.Time{}); err != nil {
				t.Errorf("import %d: %v", i, err)
			}
		}
	}()

	// The flake: a fixed sleep instead of waiting for the work.
	time.Sleep(2 * time.Millisecond)

	tasks, err := service.List(context.Background(), "ada", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(tasks) != 3 {
		t.Fatalf("imported %d tasks, want 3 - the sleep was not long enough this time", len(tasks))
	}

	waitGroup.Wait()
}
