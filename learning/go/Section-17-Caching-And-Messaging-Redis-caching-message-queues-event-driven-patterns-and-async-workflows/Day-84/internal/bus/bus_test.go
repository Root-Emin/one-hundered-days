package bus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/bus"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/outbox"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func event(id string) outbox.Event {
	return outbox.Event{EventID: id, Type: "order.created", Payload: json.RawMessage(`{"order_id":1}`)}
}

func TestHandlerFailureIsRetriedThenDeadLettered(t *testing.T) {
	broker := bus.New(quiet(), 3)

	attempts := 0

	broker.Subscribe("order.created", "shipping", func(context.Context, bus.Delivery) error {
		attempts++

		return errors.New("no shipping rate")
	})

	// The publish itself succeeds: the broker accepted the event. Consumer
	// failures are the consumer's problem.
	if err := broker.Publish(t.Context(), "order.created", event("evt-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	dead := broker.DeadLetters()

	if len(dead) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dead))
	}

	if dead[0].Delivery.EventID != "evt-1" {
		t.Errorf("dead letter event = %q, want evt-1", dead[0].Delivery.EventID)
	}
}

func TestRedeliveryIsMarkedAndEventuallySucceeds(t *testing.T) {
	broker := bus.New(quiet(), 3)

	var seen []bool

	broker.Subscribe("order.created", "shipping", func(_ context.Context, delivery bus.Delivery) error {
		seen = append(seen, delivery.Redelivered)

		if delivery.Attempt < 2 {
			return errors.New("transient")
		}

		return nil
	})

	if err := broker.Publish(t.Context(), "order.created", event("evt-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(seen) != 2 || seen[0] || !seen[1] {
		t.Errorf("redelivered flags = %v, want [false true]", seen)
	}

	if got := len(broker.DeadLetters()); got != 0 {
		t.Errorf("dead letters = %d, want 0", got)
	}
}

// One poisoned consumer must not stop the others, and must not block the
// events behind it.
func TestPoisonMessageDoesNotBlockTheQueue(t *testing.T) {
	broker := bus.New(quiet(), 2)

	healthy := 0

	broker.Subscribe("order.created", "audit", func(context.Context, bus.Delivery) error {
		healthy++

		return nil
	})

	broker.Subscribe("order.created", "shipping", func(_ context.Context, delivery bus.Delivery) error {
		if delivery.EventID == "evt-poison" {
			return errors.New("cannot handle")
		}

		return nil
	})

	for _, id := range []string{"evt-1", "evt-poison", "evt-2"} {
		if err := broker.Publish(t.Context(), "order.created", event(id)); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	if healthy != 3 {
		t.Errorf("audit saw %d events, want 3", healthy)
	}

	if got := len(broker.DeadLetters()); got != 1 {
		t.Errorf("dead letters = %d, want 1", got)
	}
}

// After the bug is fixed, the DLQ is drained back into the system.
func TestRedriveReplaysDeadLetters(t *testing.T) {
	broker := bus.New(quiet(), 2)

	broken := true
	handled := 0

	broker.Subscribe("order.created", "shipping", func(context.Context, bus.Delivery) error {
		if broken {
			return errors.New("bug")
		}

		handled++

		return nil
	})

	if err := broker.Publish(t.Context(), "order.created", event("evt-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(broker.DeadLetters()) != 1 {
		t.Fatal("expected the event in the DLQ")
	}

	broken = false

	if redriven := broker.Redrive(t.Context()); redriven != 1 {
		t.Errorf("redrove %d, want 1", redriven)
	}

	if handled != 1 {
		t.Errorf("handled = %d, want 1", handled)
	}

	if got := len(broker.DeadLetters()); got != 0 {
		t.Errorf("dead letters after redrive = %d, want 0", got)
	}
}

func TestBrokenPublishReportsAnError(t *testing.T) {
	broker := bus.New(quiet(), 1)

	broker.Break("order.created")

	if err := broker.Publish(t.Context(), "order.created", event("evt-1")); err == nil {
		t.Fatal("publish succeeded while the broker was broken")
	}

	broker.Heal("order.created")

	if err := broker.Publish(t.Context(), "order.created", event("evt-1")); err != nil {
		t.Fatalf("publish after heal: %v", err)
	}
}
