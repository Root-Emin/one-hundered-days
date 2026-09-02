package leak_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-88/internal/leak"
)

// The leaky version really does leak - asserted, so the fixed version's
// passing test means something.
func TestLeakyRequestStrandsAGoroutine(t *testing.T) {
	before := leak.Count()

	const calls = 20

	for i := 0; i < calls; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)

		if _, err := leak.LeakyRequest(ctx, time.Hour); err == nil {
			cancel()

			t.Fatal("expected a timeout")
		}

		cancel()
	}

	// These never come back: they are blocked on a send with no receiver.
	if got, settled := leak.Settle(before, 200*time.Millisecond); settled {
		t.Fatalf("goroutines settled back to %d - the leak is gone, so this test proves nothing", got)
	}

	after := leak.Count()

	if after-before < calls {
		t.Errorf("leaked %d goroutines, expected at least %d", after-before, calls)
	}

	// And the profile names the leaking function, which is how you find it in
	// production without guessing.
	stacks, err := leak.TopStacks(3)
	if err != nil {
		t.Fatalf("top stacks: %v", err)
	}

	if !strings.Contains(stacks, "LeakyRequest") {
		t.Errorf("the goroutine profile does not name the leak:\n%s", stacks)
	}
}

func TestFixedRequestCleansUpAfterATimeout(t *testing.T) {
	before := leak.Count()

	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)

		if _, err := leak.FixedRequest(ctx, 10*time.Millisecond); err == nil {
			cancel()

			t.Fatal("expected a timeout")
		}

		cancel()
	}

	if got, settled := leak.Settle(before, 5*time.Second); !settled {
		t.Errorf("goroutines did not settle back: %d, want %d", got, before)
	}
}

func TestFixedRequestStillReturnsItsValue(t *testing.T) {
	value, err := leak.FixedRequest(t.Context(), time.Millisecond)
	if err != nil {
		t.Fatalf("FixedRequest: %v", err)
	}

	if value != "done" {
		t.Errorf("value = %q, want done", value)
	}
}

// The producer closes the channel, so the range loop ends and the goroutine
// returns.
func TestFixedConsumerClosesItsChannel(t *testing.T) {
	before := leak.Count()

	total := 0

	for value := range leak.FixedConsumer(t.Context(), []int{1, 2, 3, 4}) {
		total += value
	}

	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}

	if got, settled := leak.Settle(before, time.Second); !settled {
		t.Errorf("goroutines did not settle back: %d, want %d", got, before)
	}
}

// Cancelling mid-stream must also end the producer, not just the consumer.
func TestFixedConsumerStopsOnCancel(t *testing.T) {
	before := leak.Count()

	ctx, cancel := context.WithCancel(t.Context())

	out := leak.FixedConsumer(ctx, []int{1, 2, 3, 4, 5, 6, 7, 8})

	// Take one value, then walk away.
	<-out

	cancel()

	if got, settled := leak.Settle(before, time.Second); !settled {
		t.Errorf("the producer outlived its context: %d goroutines, want %d", got, before)
	}
}

func TestFixedWorkerStopsWithItsContext(t *testing.T) {
	before := leak.Count()

	ctx, cancel := context.WithCancel(t.Context())

	done := leak.FixedWorker(ctx, time.Millisecond)

	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker did not stop within 2s of cancellation")
	}

	if got, settled := leak.Settle(before, time.Second); !settled {
		t.Errorf("goroutines did not settle back: %d, want %d", got, before)
	}
}

func TestProfileIncludesThisTest(t *testing.T) {
	profile, err := leak.Profile(1)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	if !strings.HasPrefix(profile, "goroutine profile:") {
		t.Errorf("profile does not look like a goroutine profile: %.60q", profile)
	}
}
