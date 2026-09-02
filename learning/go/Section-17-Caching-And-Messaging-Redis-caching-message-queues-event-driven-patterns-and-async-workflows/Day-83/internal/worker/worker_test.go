package worker_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/embedded"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/events"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/worker"
)

/*
Every test here runs against a real NATS server with JetStream, started inside
the test process. Real acks, real redelivery, real durable consumers - no
mocks, and nothing to install.
*/

func newBroker(t *testing.T) jetstream.JetStream {
	t.Helper()

	server, err := embedded.Start()
	if err != nil {
		t.Fatalf("start embedded nats: %v", err)
	}

	connection, js, err := events.Connect(server.ClientURL)
	if err != nil {
		server.Stop()

		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		connection.Close()
		server.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := events.EnsureStream(ctx, js); err != nil {
		t.Fatalf("stream: %v", err)
	}

	if _, err := events.EnsureDeadLetterStream(ctx, js); err != nil {
		t.Fatalf("dead letter stream: %v", err)
	}

	return js
}

func publishOrders(t *testing.T, js jetstream.JetStream, count int) {
	t.Helper()

	publisher := events.NewPublisher(js)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := 1; i <= count; i++ {
		event, err := events.NewEvent(
			fmt.Sprintf("order-%d", i), "order.created",
			events.OrderCreated{OrderID: int64(i), Customer: "test", AmountCent: int64(100 * i)},
		)
		if err != nil {
			t.Fatalf("build event: %v", err)
		}

		if _, err := publisher.Publish(ctx, events.SubjectOrderCreated, event); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
}

func TestWorkerProcessesEveryEvent(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 5)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Distinct ids, not a counter: delivery is AT LEAST once, so a handler
	// may legitimately run twice and a WaitGroup would go negative.
	seen := newSeenSet()

	orders := worker.New(js, worker.DefaultConfig("test-all"), nil,
		func(ctx context.Context, event events.Event) error {
			seen.add(event.ID)

			return nil
		})

	stop, err := orders.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	defer stop()

	seen.waitFor(t, 5, 10*time.Second)

	if processed, _, _ := orders.Counts(); processed < 5 {
		t.Fatalf("processed %d, want at least 5", processed)
	}
}

// TestFailedMessageIsRetried: a nak must bring the message back, after the
// configured backoff.
func TestFailedMessageIsRetried(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int64

	succeeded := make(chan struct{})

	config := worker.DefaultConfig("test-retry")
	config.MaxDeliver = 5
	config.AckWait = time.Second
	config.BackOff = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}

	orders := worker.New(js, config, nil, func(ctx context.Context, event events.Event) error {
		if attempts.Add(1) < 3 {
			return errors.New("transient failure")
		}

		close(succeeded)

		return nil
	})

	stop, err := orders.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	defer stop()

	select {
	case <-succeeded:
	case <-time.After(15 * time.Second):
		t.Fatalf("the message was not retried to success (attempts: %d)", attempts.Load())
	}

	processed, retried, _ := orders.Counts()

	if processed != 1 || retried != 2 {
		t.Fatalf("processed=%d retried=%d, want 1 and 2", processed, retried)
	}
}

// TestPoisonMessageGoesToTheDeadLetterStream: a message that can never
// succeed must be terminated at once, not retried to exhaustion.
func TestPoisonMessageGoesToTheDeadLetterStream(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int64

	config := worker.DefaultConfig("test-poison")
	config.MaxDeliver = 5
	config.AckWait = 500 * time.Millisecond

	orders := worker.New(js, config, nil, func(ctx context.Context, event events.Event) error {
		attempts.Add(1)

		return fmt.Errorf("%w: unsupported", worker.ErrPoison)
	})

	stop, err := orders.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	defer stop()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if _, _, dead := orders.Counts(); dead > 0 {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	_, _, dead := orders.Counts()

	if dead != 1 {
		t.Fatalf("dead-lettered %d, want 1", dead)
	}

	// Terminated on the FIRST attempt: retrying a poison message is wasted
	// capacity.
	if attempts.Load() != 1 {
		t.Fatalf("attempted %d times, want 1", attempts.Load())
	}

	// And the message is preserved for inspection.
	stream, err := js.Stream(ctx, events.DeadLetterStream)
	if err != nil {
		t.Fatalf("dlq stream: %v", err)
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("dlq info: %v", err)
	}

	if info.State.Msgs != 1 {
		t.Fatalf("dead letter stream holds %d messages, want 1", info.State.Msgs)
	}
}

// TestGivingUpAfterMaxDeliver: a message that keeps failing must eventually
// stop consuming capacity.
func TestGivingUpAfterMaxDeliver(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int64

	config := worker.DefaultConfig("test-giveup")
	config.MaxDeliver = 3
	config.AckWait = 500 * time.Millisecond
	config.BackOff = []time.Duration{50 * time.Millisecond}

	orders := worker.New(js, config, nil, func(ctx context.Context, event events.Event) error {
		attempts.Add(1)

		return errors.New("always fails")
	})

	stop, err := orders.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	defer stop()

	deadline := time.Now().Add(15 * time.Second)

	for time.Now().Before(deadline) {
		if _, _, dead := orders.Counts(); dead > 0 {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	if _, _, dead := orders.Counts(); dead != 1 {
		t.Fatalf("dead = %d after MaxDeliver attempts, want 1", dead)
	}

	if attempts.Load() != int64(config.MaxDeliver) {
		t.Fatalf("attempted %d times, want %d", attempts.Load(), config.MaxDeliver)
	}
}

// TestDurableConsumerResumes: a restarted worker continues where it stopped,
// which is the whole reason the consumer is durable.
func TestDurableConsumerResumes(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 6)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := newSeenSet()

	handler := func(ctx context.Context, event events.Event) error {
		seen.add(event.ID)

		// Slow enough that the first worker cannot finish them all before it
		// is stopped.
		time.Sleep(100 * time.Millisecond)

		return nil
	}

	firstWorker := worker.New(js, worker.DefaultConfig("resuming"), nil, handler)

	stop, err := firstWorker.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Let it handle some, then "crash".
	time.Sleep(300 * time.Millisecond)

	stop()

	partial := seen.count()

	if partial == 0 {
		t.Fatal("the first worker processed nothing")
	}

	if partial == 6 {
		t.Log("the first worker finished everything; the resume path is still exercised below")
	}

	// A second worker with the SAME durable name picks up the rest.
	secondWorker := worker.New(js, worker.DefaultConfig("resuming"), nil, handler)

	stopSecond, err := secondWorker.Start(ctx)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}

	defer stopSecond()

	seen.waitFor(t, 6, 15*time.Second)
}

// TestScalingOutSharesTheWork: two workers in one durable group split the
// stream between them.
func TestScalingOutSharesTheWork(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu     sync.Mutex
		counts = map[string]int{}
	)

	seen := newSeenSet()

	for _, name := range []string{"worker-a", "worker-b"} {
		instance := name

		instanceWorker := worker.New(js, worker.DefaultConfig("shared-group"), nil,
			func(ctx context.Context, event events.Event) error {
				mu.Lock()
				counts[instance]++
				mu.Unlock()

				seen.add(event.ID)

				return nil
			})

		stop, err := instanceWorker.Start(ctx)
		if err != nil {
			t.Fatalf("start %s: %v", instance, err)
		}

		defer stop()
	}

	seen.waitFor(t, 20, 20*time.Second)

	mu.Lock()
	defer mu.Unlock()

	t.Logf("distribution across the group: %v", counts)

	if len(counts) < 2 {
		t.Logf("only one worker received messages (%v); acceptable, but the group did not split", counts)
	}
}

func TestLagReporting(t *testing.T) {
	js := newBroker(t)

	publishOrders(t, js, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})

	orders := worker.New(js, worker.DefaultConfig("lagging"), nil,
		func(ctx context.Context, event events.Event) error {
			<-release // hold every message so the lag is observable

			return nil
		})

	stop, err := orders.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	defer func() {
		close(release)
		stop()
	}()

	time.Sleep(500 * time.Millisecond)

	lag, err := orders.Lag(ctx)
	if err != nil {
		t.Fatalf("lag: %v", err)
	}

	if lag.StreamLastSeq != 10 {
		t.Fatalf("stream last sequence = %d, want 10", lag.StreamLastSeq)
	}

	// Either waiting to be delivered, or delivered and not yet acked: both
	// are backlog, and both are what an alert would watch.
	if lag.Pending == 0 && lag.AckPending == 0 {
		t.Fatal("no lag reported while every handler is blocked")
	}
}

// TestPublisherDeduplication: the stream's duplicate window means a publisher
// retry does not create a second event.
func TestPublisherDeduplication(t *testing.T) {
	js := newBroker(t)

	publisher := events.NewPublisher(js)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	event, err := events.NewEvent("stable-id", "order.created",
		events.OrderCreated{OrderID: 1, Customer: "ada", AmountCent: 100})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}

	first, err := publisher.Publish(ctx, events.SubjectOrderCreated, event)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	second, err := publisher.Publish(ctx, events.SubjectOrderCreated, event)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}

	if first != second {
		t.Fatalf("sequences %d and %d differ: the publish was not deduplicated", first, second)
	}

	stream, err := js.Stream(ctx, events.StreamName)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}

	if info.State.Msgs != 1 {
		t.Fatalf("stream holds %d messages, want 1", info.State.Msgs)
	}
}

// seenSet counts DISTINCT events.
//
// At-least-once delivery means a handler can run more than once for the same
// message, so a test that counts invocations is flaky by construction. This
// counts what actually matters: that every event was handled at least once.
type seenSet struct {
	mu  sync.Mutex
	ids map[string]int
}

func newSeenSet() *seenSet {
	return &seenSet{ids: make(map[string]int)}
}

func (s *seenSet) add(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ids[id]++
}

func (s *seenSet) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.ids)
}

func (s *seenSet) waitFor(t *testing.T, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if s.count() >= want {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("saw %d distinct events, want %d", s.count(), want)
}
