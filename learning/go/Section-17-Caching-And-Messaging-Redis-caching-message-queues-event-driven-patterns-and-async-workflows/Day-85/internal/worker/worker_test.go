package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/queue"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/worker"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	db, err := store.Open("file:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	return store.New(db)
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func placeOrder(t *testing.T, dataStore *store.Store) (store.Order, queue.Delivery) {
	t.Helper()

	product, err := dataStore.CreateProduct(t.Context(), "keyboard", 12000)
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	order, err := dataStore.PlaceOrder(t.Context(), product.ID, 2)
	if err != nil {
		t.Fatalf("place order: %v", err)
	}

	events, err := dataStore.UnpublishedEvents(t.Context(), 10)
	if err != nil {
		t.Fatalf("unpublished events: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("unpublished events = %d, want 1", len(events))
	}

	return order, queue.Delivery{
		EventID: events[0].EventID,
		Type:    events[0].Type,
		Payload: events[0].Payload,
		Attempt: 1,
	}
}

// Task 3, stated directly: deliver the same event twice, verify the outcome.
func TestDuplicateDeliveryWritesOneReceipt(t *testing.T) {
	dataStore := newStore(t)
	receipts := worker.New(dataStore, quiet())

	order, delivery := placeOrder(t, dataStore)

	for attempt := 1; attempt <= 2; attempt++ {
		delivery.Attempt = attempt

		// Both deliveries must succeed from the broker's point of view. A
		// duplicate that returns an error would be redelivered forever.
		if err := receipts.HandleOrderPlaced(t.Context(), delivery); err != nil {
			t.Fatalf("delivery %d: %v", attempt, err)
		}
	}

	count, err := dataStore.ReceiptCount(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("receipt count: %v", err)
	}

	if count != 1 {
		t.Errorf("receipts = %d, want exactly 1 after two deliveries", count)
	}

	processed, duplicates := receipts.Stats()

	if processed != 1 || duplicates != 1 {
		t.Errorf("stats = (processed %d, duplicates %d), want (1, 1)", processed, duplicates)
	}

	confirmed, err := dataStore.Order(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}

	if confirmed.Status != "confirmed" {
		t.Errorf("status = %q, want confirmed", confirmed.Status)
	}
}

// A payload that will never parse must not be retried forever.
func TestUndecodablePayloadFails(t *testing.T) {
	dataStore := newStore(t)
	receipts := worker.New(dataStore, quiet())

	err := receipts.HandleOrderPlaced(t.Context(), queue.Delivery{
		EventID: "evt-broken",
		Type:    "order.placed",
		Payload: []byte("not json"),
	})

	if err == nil {
		t.Fatal("expected a decode error")
	}
}

// End to end through the relay: the outbox row becomes a receipt, and the
// deliberate duplicate delivery changes nothing.
func TestRelayToWorkerIsIdempotentEndToEnd(t *testing.T) {
	dataStore := newStore(t)

	bus := queue.NewBus(quiet())
	bus.DeliverTwice(true)

	receipts := worker.New(dataStore, quiet())
	receipts.Register(bus)

	relay := queue.NewRelay(dataStore, bus, time.Millisecond, quiet())

	order, _ := placeOrder(t, dataStore)

	if err := relay.DrainOnce(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	pending, err := dataStore.PendingEventCount(t.Context())
	if err != nil {
		t.Fatalf("pending events: %v", err)
	}

	if pending != 0 {
		t.Errorf("pending events = %d, want 0", pending)
	}

	count, err := dataStore.ReceiptCount(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("receipt count: %v", err)
	}

	if count != 1 {
		t.Errorf("receipts = %d, want 1", count)
	}

	delivered, failures := bus.Stats()

	if delivered != 2 || failures != 0 {
		t.Errorf("bus stats = (delivered %d, failures %d), want (2, 0)", delivered, failures)
	}
}

// A crash between publishing and marking the row republishes the event. The
// consumer's claim is what makes that safe.
func TestRepublishAfterAMissedMarkIsHarmless(t *testing.T) {
	dataStore := newStore(t)

	bus := queue.NewBus(quiet())

	receipts := worker.New(dataStore, quiet())
	receipts.Register(bus)

	order, delivery := placeOrder(t, dataStore)

	// First publish lands, but imagine the process dies before MarkPublished.
	if err := bus.Publish(t.Context(), store.Event{
		EventID: delivery.EventID,
		Type:    delivery.Type,
		Payload: delivery.Payload,
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The row is still unpublished, so the next relay tick sends it again.
	relay := queue.NewRelay(dataStore, bus, time.Millisecond, quiet())

	if err := relay.DrainOnce(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}

	count, err := dataStore.ReceiptCount(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("receipt count: %v", err)
	}

	if count != 1 {
		t.Errorf("receipts = %d, want 1 despite the republish", count)
	}
}

// A failing handler leaves the event unpublished so it is retried, and leaves
// no claim behind that would skip the retry.
func TestHandlerFailureKeepsTheEventPending(t *testing.T) {
	dataStore := newStore(t)

	bus := queue.NewBus(quiet())

	failing := errors.New("downstream unavailable")
	broken := true

	bus.Subscribe("order.placed", "flaky", func(ctx context.Context, delivery queue.Delivery) error {
		if broken {
			return failing
		}

		return worker.New(dataStore, quiet()).HandleOrderPlaced(ctx, delivery)
	})

	order, _ := placeOrder(t, dataStore)

	relay := queue.NewRelay(dataStore, bus, time.Millisecond, quiet())

	if err := relay.DrainOnce(t.Context()); !errors.Is(err, failing) {
		t.Fatalf("drain error = %v, want the handler's failure", err)
	}

	pending, err := dataStore.PendingEventCount(t.Context())
	if err != nil {
		t.Fatalf("pending events: %v", err)
	}

	if pending != 1 {
		t.Fatalf("pending events = %d, want 1 (the failed event must be retried)", pending)
	}

	broken = false

	if err := relay.DrainOnce(t.Context()); err != nil {
		t.Fatalf("drain after recovery: %v", err)
	}

	count, err := dataStore.ReceiptCount(t.Context(), order.ID)
	if err != nil {
		t.Fatalf("receipt count: %v", err)
	}

	if count != 1 {
		t.Errorf("receipts = %d, want 1", count)
	}
}

// Sanity check on the payload contract between producer and consumer: the
// worker reads fields the store actually writes.
func TestEventPayloadCarriesTheOrder(t *testing.T) {
	dataStore := newStore(t)

	order, delivery := placeOrder(t, dataStore)

	var decoded store.Order

	if err := json.Unmarshal(delivery.Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if decoded.ID != order.ID || decoded.AmountCent != order.AmountCent {
		t.Errorf("payload = %+v, want it to match %+v", decoded, order)
	}
}
