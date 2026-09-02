package main

import (
	"context"
	"testing"
	"time"
)

/*
The service locator's failure mode, demonstrated.

These tests pass only because they are careful: they cannot run in parallel,
they must reset a global between cases, and a forgotten registration panics
instead of failing to compile. The tests in service_test.go need none of that
discipline, because nothing is shared.
*/

func TestLocatorNeedsGlobalSetup(t *testing.T) {
	// No t.Parallel: the registry is shared process-wide, so a parallel
	// sibling would swap dependencies mid-test.
	ResetRegistry()

	t.Cleanup(ResetRegistry)

	Register("users", NewMemoryUserRepository())
	Register("clock", SystemClock{})
	Register("ids", &sequenceIDs{})
	Register("mailer", NoopMailer{})

	user, err := LocatorSignup{}.Signup(context.Background(), "ada@example.com", "Ada", "pro")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}

	if user.ID == "" {
		t.Fatal("no id assigned")
	}
}

// TestLocatorPanicsOnMissingDependency is the 3am failure: nothing in the type
// system, the compiler or the startup path said this was missing.
func TestLocatorPanicsOnMissingDependency(t *testing.T) {
	ResetRegistry()

	t.Cleanup(ResetRegistry)

	Register("users", NewMemoryUserRepository())
	Register("clock", SystemClock{})
	Register("ids", &sequenceIDs{})
	// "mailer" is deliberately not registered.

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected a panic from the unresolved dependency")
		}
	}()

	_, _ = LocatorSignup{}.Signup(context.Background(), "ada@example.com", "Ada", "free")
}

// TestLocatorLeaksStateBetweenTests shows the pollution directly: a fake
// registered by one test is still there for the next one.
func TestLocatorLeaksStateBetweenTests(t *testing.T) {
	ResetRegistry()

	shared := NewMemoryUserRepository()

	Register("users", shared)
	Register("clock", &fakeClock{now: time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)})
	Register("ids", &sequenceIDs{})
	Register("mailer", NoopMailer{})

	if _, err := (LocatorSignup{}).Signup(context.Background(), "ada@example.com", "Ada", "free"); err != nil {
		t.Fatalf("first signup: %v", err)
	}

	// A second "test", written by somebody else, forgets to reset. It gets
	// the previous test's repository - and its rows.
	count, err := shared.Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if count != 1 {
		t.Fatalf("count = %d", count)
	}

	if _, err := (LocatorSignup{}).Signup(context.Background(), "ada@example.com", "Ada", "free"); err == nil {
		t.Fatal("the duplicate was accepted - which means the state was NOT shared, so this demo is broken")
	}

	// Cleaning up is a manual, easily forgotten step. Every locator test needs
	// this line; no constructor-injected test needs anything.
	ResetRegistry()
}
