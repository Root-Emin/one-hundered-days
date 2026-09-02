package outbox_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-84/internal/outbox"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)", t.Name()))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	db.SetMaxOpenConns(1)

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if _, err := db.Exec(outbox.Schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return db
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingPublisher fails until healed, so a test can prove the outbox holds
// events across an outage.
type recordingPublisher struct {
	mu        sync.Mutex
	fail      error
	published []outbox.Event
}

func (p *recordingPublisher) Publish(_ context.Context, _ string, event outbox.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fail != nil {
		return p.fail
	}

	p.published = append(p.published, event)

	return nil
}

func (p *recordingPublisher) events() []outbox.Event {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]outbox.Event(nil), p.published...)
}

func (p *recordingPublisher) breakIt(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.fail = err
}

// The whole reason the pattern exists: the order and its event are one write.
func TestCreateOrderWritesRowAndEventAtomically(t *testing.T) {
	db := newDB(t)
	store := outbox.NewStore(db)

	order, err := store.CreateOrder(t.Context(), "ada", 4200)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	var orders int

	if err := db.QueryRow(`SELECT COUNT(*) FROM orders WHERE id = ?;`, order.ID).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}

	if orders != 1 {
		t.Fatalf("orders = %d, want 1", orders)
	}

	events, err := store.Unpublished(t.Context(), 10)
	if err != nil {
		t.Fatalf("unpublished: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("unpublished events = %d, want 1", len(events))
	}

	if got, want := events[0].EventID, fmt.Sprintf("order-%d-created", order.ID); got != want {
		t.Errorf("event id = %q, want %q", got, want)
	}

	if events[0].Type != "order.created" {
		t.Errorf("event type = %q, want order.created", events[0].Type)
	}
}

// A failed transaction must leave neither the row nor the event behind - the
// half-written state is exactly what the pattern prevents.
func TestFailedTransactionLeavesNothing(t *testing.T) {
	db := newDB(t)
	store := outbox.NewStore(db)

	// A cancelled context aborts the insert.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.CreateOrder(ctx, "ada", 1); err == nil {
		t.Fatal("create order with a cancelled context succeeded, want failure")
	}

	pending, err := store.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}

	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}

	var orders int

	if err := db.QueryRow(`SELECT COUNT(*) FROM orders;`).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}

	if orders != 0 {
		t.Errorf("orders = %d, want 0", orders)
	}
}

func TestRelayHoldsEventsWhileTheBrokerIsDown(t *testing.T) {
	db := newDB(t)
	store := outbox.NewStore(db)

	publisher := &recordingPublisher{}
	publisher.breakIt(errors.New("broker unavailable"))

	relay := outbox.NewRelay(store, publisher, time.Millisecond, quietLogger())

	for _, customer := range []string{"ada", "grace"} {
		if _, err := store.CreateOrder(t.Context(), customer, 100); err != nil {
			t.Fatalf("create order: %v", err)
		}
	}

	if err := relay.Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	pending, err := store.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}

	if pending != 2 {
		t.Fatalf("pending during the outage = %d, want 2 (nothing may be lost)", pending)
	}

	if published, failed := relay.Counts(); published != 0 || failed != 1 {
		t.Errorf("counts = (%d, %d), want (0, 1)", published, failed)
	}

	publisher.breakIt(nil)

	if err := relay.Drain(t.Context()); err != nil {
		t.Fatalf("drain after recovery: %v", err)
	}

	if pending, err = store.PendingCount(t.Context()); err != nil {
		t.Fatalf("pending count: %v", err)
	}

	if pending != 0 {
		t.Errorf("pending after recovery = %d, want 0", pending)
	}

	if got := len(publisher.events()); got != 2 {
		t.Errorf("published %d events, want 2", got)
	}
}

// Publishing out of order lets a consumer see an update before the create.
func TestRelayPublishesInOrderAndStopsAtTheFirstFailure(t *testing.T) {
	db := newDB(t)
	store := outbox.NewStore(db)

	var ids []int64

	for i := 0; i < 3; i++ {
		order, err := store.CreateOrder(t.Context(), fmt.Sprintf("customer-%d", i), int64(i))
		if err != nil {
			t.Fatalf("create order: %v", err)
		}

		ids = append(ids, order.ID)
	}

	// Fail on the second event only.
	publisher := &stubPublisher{failOn: fmt.Sprintf("order-%d-created", ids[1])}

	relay := outbox.NewRelay(store, publisher, time.Millisecond, quietLogger())

	if err := relay.Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if got := publisher.seen; len(got) != 2 {
		t.Fatalf("publisher saw %v, want to stop after the failing event", got)
	}

	// Two events are still pending: the failed one and the one behind it.
	pending, err := store.PendingCount(t.Context())
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}

	if pending != 2 {
		t.Errorf("pending = %d, want 2", pending)
	}

	events, err := store.Unpublished(t.Context(), 10)
	if err != nil {
		t.Fatalf("unpublished: %v", err)
	}

	if events[0].Attempts != 1 {
		t.Errorf("attempts on the failed event = %d, want 1", events[0].Attempts)
	}

	publisher.failOn = ""

	if err := relay.Drain(t.Context()); err != nil {
		t.Fatalf("drain after recovery: %v", err)
	}

	want := []string{
		fmt.Sprintf("order-%d-created", ids[0]),
		fmt.Sprintf("order-%d-created", ids[1]),
		fmt.Sprintf("order-%d-created", ids[1]),
		fmt.Sprintf("order-%d-created", ids[2]),
	}

	for i, id := range want {
		if publisher.seen[i] != id {
			t.Fatalf("delivery %d = %q, want %q (order must be preserved)", i, publisher.seen[i], id)
		}
	}
}

type stubPublisher struct {
	failOn string
	seen   []string
}

func (p *stubPublisher) Publish(_ context.Context, _ string, event outbox.Event) error {
	p.seen = append(p.seen, event.EventID)

	if event.EventID == p.failOn {
		return errors.New("publish rejected")
	}

	return nil
}

func TestRelayRunStopsWithTheContext(t *testing.T) {
	db := newDB(t)
	store := outbox.NewStore(db)

	publisher := &recordingPublisher{}
	relay := outbox.NewRelay(store, publisher, time.Millisecond, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		defer close(done)

		relay.Run(ctx)
	}()

	if _, err := store.CreateOrder(t.Context(), "ada", 1); err != nil {
		t.Fatalf("create order: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)

	for {
		if len(publisher.events()) == 1 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("relay did not publish within the deadline")
		}

		time.Sleep(2 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not stop when its context was cancelled")
	}
}
