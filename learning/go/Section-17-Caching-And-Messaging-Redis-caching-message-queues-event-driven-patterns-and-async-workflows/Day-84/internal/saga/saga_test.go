package saga_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/saga"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// tracker records the order in which steps ran, which is the property the
// whole pattern rests on.
type tracker struct {
	calls []string
}

func (tr *tracker) step(name string, executeErr, compensateErr error) saga.Step {
	return saga.Step{
		Name: name,
		Execute: func(context.Context, *saga.Data) error {
			tr.calls = append(tr.calls, "do:"+name)

			return executeErr
		},
		Compensate: func(context.Context, *saga.Data) error {
			tr.calls = append(tr.calls, "undo:"+name)

			return compensateErr
		},
	}
}

func TestHappyPathRunsEveryStepInOrder(t *testing.T) {
	tr := &tracker{}

	workflow := saga.New("place-order", quiet(),
		tr.step("reserve", nil, nil),
		tr.step("charge", nil, nil),
		tr.step("ship", nil, nil),
	)

	if err := workflow.Run(t.Context(), saga.NewData()); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := []string{"do:reserve", "do:charge", "do:ship"}

	if strings.Join(tr.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", tr.calls, want)
	}

	if workflow.State() != saga.StateCompleted {
		t.Errorf("state = %s, want %s", workflow.State(), saga.StateCompleted)
	}
}

// Compensation runs in reverse: the last completed step is undone first,
// because later steps may depend on earlier ones.
func TestFailureCompensatesInReverseOrder(t *testing.T) {
	tr := &tracker{}

	boom := errors.New("carrier rejected the address")

	workflow := saga.New("place-order", quiet(),
		tr.step("reserve", nil, nil),
		tr.step("charge", nil, nil),
		tr.step("ship", boom, nil),
	)

	err := workflow.Run(t.Context(), saga.NewData())

	if !errors.Is(err, saga.ErrCompensated) {
		t.Fatalf("error = %v, want ErrCompensated", err)
	}

	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want it to wrap the step's failure", err)
	}

	// The failing step is never compensated: it did not complete.
	want := []string{"do:reserve", "do:charge", "do:ship", "undo:charge", "undo:reserve"}

	if strings.Join(tr.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", tr.calls, want)
	}

	if workflow.State() != saga.StateCompensated {
		t.Errorf("state = %s, want %s", workflow.State(), saga.StateCompensated)
	}
}

func TestFirstStepFailureCompensatesNothing(t *testing.T) {
	tr := &tracker{}

	workflow := saga.New("place-order", quiet(),
		tr.step("reserve", errors.New("out of stock"), nil),
		tr.step("charge", nil, nil),
	)

	if err := workflow.Run(t.Context(), saga.NewData()); !errors.Is(err, saga.ErrCompensated) {
		t.Fatalf("error = %v, want ErrCompensated", err)
	}

	want := []string{"do:reserve"}

	if strings.Join(tr.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v (nothing completed, so nothing to undo)", tr.calls, want)
	}
}

// The state that needs a human: the work failed AND the undo failed.
func TestFailedCompensationEndsInStateFailed(t *testing.T) {
	tr := &tracker{}

	refundFailed := errors.New("refund API unreachable")

	workflow := saga.New("place-order", quiet(),
		tr.step("reserve", nil, nil),
		tr.step("charge", nil, refundFailed),
		tr.step("ship", errors.New("carrier down"), nil),
	)

	err := workflow.Run(t.Context(), saga.NewData())

	if err == nil {
		t.Fatal("expected an error")
	}

	if errors.Is(err, saga.ErrCompensated) {
		t.Error("a failed compensation must not report a clean rollback")
	}

	if !errors.Is(err, refundFailed) {
		t.Errorf("error = %v, want it to wrap the compensation failure", err)
	}

	if workflow.State() != saga.StateFailed {
		t.Errorf("state = %s, want %s", workflow.State(), saga.StateFailed)
	}

	// Compensation keeps going after a failure: undoing some steps beats
	// leaving all of them half-done.
	if strings.Join(tr.calls, ",") != "do:reserve,do:charge,do:ship,undo:charge,undo:reserve" {
		t.Errorf("calls = %v, want compensation to continue past the failure", tr.calls)
	}
}

// Compensation must run even when the caller's context is already cancelled -
// a cancelled context is often *why* the step failed.
func TestCompensationRunsWithACancelledContext(t *testing.T) {
	compensated := false

	workflow := saga.New("place-order", quiet(),
		saga.Step{
			Name:    "reserve",
			Execute: func(context.Context, *saga.Data) error { return nil },
			Compensate: func(ctx context.Context, _ *saga.Data) error {
				compensated = true

				if err := ctx.Err(); err != nil {
					t.Errorf("compensation context was already cancelled: %v", err)
				}

				return nil
			},
		},
		saga.Step{
			Name: "charge",
			Execute: func(ctx context.Context, _ *saga.Data) error {
				return ctx.Err()
			},
		},
	)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := workflow.Run(ctx, saga.NewData()); !errors.Is(err, saga.ErrCompensated) {
		t.Fatalf("error = %v, want ErrCompensated", err)
	}

	if !compensated {
		t.Fatal("compensation was skipped because the caller's context was cancelled")
	}
}

// Data carries state forward, and the compensation reads what its step wrote.
func TestDataFlowsBetweenSteps(t *testing.T) {
	var released string

	workflow := saga.New("place-order", quiet(),
		saga.Step{
			Name: "reserve",
			Execute: func(_ context.Context, data *saga.Data) error {
				data.Set("reservation", "res-991")

				return nil
			},
			Compensate: func(_ context.Context, data *saga.Data) error {
				value, found := data.Get("reservation")
				if !found {
					return errors.New("reservation missing")
				}

				released = value.(string)

				return nil
			},
		},
		saga.Step{
			Name: "charge",
			Execute: func(_ context.Context, data *saga.Data) error {
				if _, found := data.Get("reservation"); !found {
					return errors.New("no reservation to charge against")
				}

				return errors.New("card declined")
			},
		},
	)

	if err := workflow.Run(t.Context(), saga.NewData()); !errors.Is(err, saga.ErrCompensated) {
		t.Fatalf("error = %v, want ErrCompensated", err)
	}

	if released != "res-991" {
		t.Errorf("released = %q, want res-991", released)
	}
}

func TestHistoryRecordsEveryAction(t *testing.T) {
	tr := &tracker{}

	workflow := saga.New("place-order", quiet(),
		tr.step("reserve", nil, nil),
		tr.step("charge", errors.New("declined"), nil),
	)

	_ = workflow.Run(t.Context(), saga.NewData())

	history := workflow.History()

	if len(history) != 3 {
		t.Fatalf("history has %d entries, want 3", len(history))
	}

	if history[1].Err == nil {
		t.Error("the failing step's entry has no error recorded")
	}

	if history[2].Action != "compensate" || history[2].Step != "reserve" {
		t.Errorf("last entry = %+v, want a compensation of reserve", history[2])
	}
}

func TestDescribeMarksStepsThatCannotBeUndone(t *testing.T) {
	workflow := saga.New("notify", quiet(),
		saga.Step{
			Name:    "send-email",
			Execute: func(context.Context, *saga.Data) error { return nil },
		},
	)

	description := workflow.Describe()

	if !strings.Contains(description, "cannot be undone") {
		t.Errorf("describe = %q, want it to flag the step with no compensation", description)
	}
}
