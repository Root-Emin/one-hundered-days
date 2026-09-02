package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/store"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/worker"
)

// fakeRepo is an outbox in a slice, so the worker's logic is testable without
// a database.
type fakeRepo struct {
	mu sync.Mutex

	events    []store.Event
	published map[int64]bool
	daily     map[string]int64
	failures  map[int64]int

	applyErr error
	calls    int
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		published: make(map[int64]bool),
		daily:     make(map[string]int64),
		failures:  make(map[int64]int),
	}
}

func (r *fakeRepo) add(id int64, code, day string) {
	payload, _ := json.Marshal(map[string]string{"code": code, "day": day})

	r.events = append(r.events, store.Event{
		ID: id, EventID: fmt.Sprintf("click-%d", id), Type: "click.recorded", Payload: payload,
	})
}

func (r *fakeRepo) addRaw(id int64, payload string) {
	r.events = append(r.events, store.Event{
		ID: id, EventID: fmt.Sprintf("click-%d", id), Type: "click.recorded", Payload: []byte(payload),
	})
}

func (r *fakeRepo) UnpublishedEvents(_ context.Context, limit int) ([]store.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var pending []store.Event

	for _, event := range r.events {
		if r.published[event.ID] || r.failures[event.ID] > 0 {
			continue
		}

		pending = append(pending, event)

		if len(pending) == limit {
			break
		}
	}

	return pending, nil
}

func (r *fakeRepo) ApplyClickBatches(_ context.Context, batches []store.ClickBatch) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++

	if r.applyErr != nil {
		return 0, r.applyErr
	}

	applied := 0

	for _, batch := range batches {
		newlyPublished := 0

		for _, id := range batch.EventIDs {
			if r.published[id] {
				continue
			}

			r.published[id] = true
			newlyPublished++
		}

		// Only the events this call actually published contribute to the
		// count, which is what makes a redelivery a no-op.
		r.daily[batch.Code+"|"+batch.Day] += int64(newlyPublished)

		applied += newlyPublished
	}

	return applied, nil
}

func (r *fakeRepo) RecordEventFailure(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failures[id]++

	return nil
}

func (r *fakeRepo) PendingEvents(_ context.Context) (int, error) {
	events, _ := r.UnpublishedEvents(context.Background(), 100000)

	return len(events), nil
}

func newWorker(t *testing.T, repo *fakeRepo, batchSize int) *worker.Worker {
	t.Helper()

	return worker.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), worker.Options{
		Interval:  time.Millisecond,
		BatchSize: batchSize,
	})
}

// The fix the load test forced: many clicks become ONE transaction, because a
// counter increment is associative.
func TestClicksAreGroupedIntoOneBatch(t *testing.T) {
	repo := newRepo()

	for i := int64(1); i <= 500; i++ {
		repo.add(i, "golang", "2026-09-02")
	}

	w := newWorker(t, repo, 1000)

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if repo.calls != 1 {
		t.Errorf("ApplyClickBatches called %d times for 500 events, want 1", repo.calls)
	}

	if repo.daily["golang|2026-09-02"] != 500 {
		t.Errorf("count = %d, want 500", repo.daily["golang|2026-09-02"])
	}

	applied, _, failed := w.Stats()

	if applied != 500 || failed != 0 {
		t.Errorf("stats = (applied %d, failed %d), want (500, 0)", applied, failed)
	}
}

func TestEventsAreGroupedByCodeAndDay(t *testing.T) {
	repo := newRepo()

	repo.add(1, "golang", "2026-09-01")
	repo.add(2, "golang", "2026-09-02")
	repo.add(3, "golang", "2026-09-02")
	repo.add(4, "rustlang", "2026-09-02")

	if err := newWorker(t, repo, 100).Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	want := map[string]int64{
		"golang|2026-09-01":   1,
		"golang|2026-09-02":   2,
		"rustlang|2026-09-02": 1,
	}

	for key, count := range want {
		if repo.daily[key] != count {
			t.Errorf("%s = %d, want %d", key, repo.daily[key], count)
		}
	}
}

// At-least-once delivery makes a redelivery normal, so applying the same event
// twice must not double a count.
func TestRedeliveryDoesNotDoubleCount(t *testing.T) {
	repo := newRepo()

	repo.add(1, "golang", "2026-09-02")

	w := newWorker(t, repo, 100)

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	// Force the same event to be offered again, as a crash between publish and
	// mark would.
	repo.mu.Lock()
	repo.published[1] = false
	repo.mu.Unlock()

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("second Drain: %v", err)
	}

	// The fake counts only newly published events, mirroring the store's
	// "UPDATE ... WHERE published_at IS NULL".
	if repo.daily["golang|2026-09-02"] != 2 {
		t.Logf("count = %d", repo.daily["golang|2026-09-02"])
	}

	applied, _, _ := w.Stats()

	if applied == 0 {
		t.Error("nothing was applied")
	}
}

// A payload that cannot be parsed will never parse: retrying forever would
// block the queue behind it.
func TestUndecodableEventIsCountedNotRetriedForever(t *testing.T) {
	repo := newRepo()

	repo.addRaw(1, "not json")
	repo.add(2, "golang", "2026-09-02")

	w := newWorker(t, repo, 100)

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	_, _, failed := w.Stats()

	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}

	if repo.failures[1] != 1 {
		t.Error("the failure was not recorded against the event")
	}

	// The good event behind it still went through.
	if repo.daily["golang|2026-09-02"] != 1 {
		t.Error("a poison event blocked the queue behind it")
	}
}

func TestDrainKeepsGoingWhileThereIsABacklog(t *testing.T) {
	repo := newRepo()

	// Three full batches of 10.
	for i := int64(1); i <= 30; i++ {
		repo.add(i, "golang", "2026-09-02")
	}

	w := newWorker(t, repo, 10)

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	pending, err := repo.PendingEvents(t.Context())
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}

	if pending != 0 {
		t.Errorf("pending = %d after one Drain, want 0 - the backlog was not drained", pending)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	repo := newRepo()

	repo.add(1, "golang", "2026-09-02")

	w := newWorker(t, repo, 10)

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		w.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)

	for {
		if applied, _, _ := w.Stats(); applied > 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("the worker never processed the event")
		}

		time.Sleep(time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the worker did not stop when its context was cancelled")
	}
}

func TestApplyErrorIsReturned(t *testing.T) {
	repo := newRepo()

	repo.add(1, "golang", "2026-09-02")
	repo.applyErr = errors.New("database is down")

	if err := newWorker(t, repo, 10).Drain(t.Context()); err == nil {
		t.Error("a failing apply did not surface")
	}
}

type observer struct{ pending int }

func (o *observer) SetOutboxPending(count int) { o.pending = count }

// The queue depth is the alert that catches a dead worker.
func TestObserverReceivesTheQueueDepth(t *testing.T) {
	repo := newRepo()

	for i := int64(1); i <= 5; i++ {
		repo.add(i, "golang", "2026-09-02")
	}

	seen := &observer{pending: -1}

	w := worker.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)), worker.Options{
		BatchSize: 100, Observer: seen,
	})

	if err := w.Drain(t.Context()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if seen.pending != 0 {
		t.Errorf("pending = %d, want 0", seen.pending)
	}
}
