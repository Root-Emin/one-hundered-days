// Package events defines the domain events and the JetStream plumbing that
// carries them.
//
// NATS in one paragraph: core NATS is fire-and-forget pub/sub - fast, and a
// message with no listener is gone. JetStream adds a persistent STREAM in
// front of subjects, with acknowledgment, redelivery and replay. Anything that
// matters uses JetStream.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Subjects. NATS subjects are hierarchical and wildcards match them:
//
//	orders.created      one subject
//	orders.*            any single token: created, shipped, cancelled
//	orders.>            everything below orders
//
// Naming them <entity>.<past-tense-verb> keeps the hierarchy usable.
const (
	SubjectOrderCreated   = "orders.created"
	SubjectOrderCancelled = "orders.cancelled"
	SubjectAll            = "orders.>"

	StreamName = "ORDERS"

	// The dead letter subject lives OUTSIDE orders.> : two JetStream streams
	// may not claim overlapping subjects, and a dead letter republished into
	// the stream it came from would loop forever.
	SubjectDeadLetter = "dlq.orders"
	DeadLetterStream  = "ORDERS_DLQ"
)

// Event is the envelope every message shares.
//
// The metadata is not decoration: ID is the deduplication key, OccurredAt
// lets a consumer detect stale events, and Version is what makes changing the
// payload possible later.
type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Version    int             `json:"version"`
	OccurredAt time.Time       `json:"occurred_at"`
	TraceID    string          `json:"trace_id,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

type OrderCreated struct {
	OrderID    int64  `json:"order_id"`
	Customer   string `json:"customer"`
	AmountCent int64  `json:"amount_cents"`
}

type OrderCancelled struct {
	OrderID int64  `json:"order_id"`
	Reason  string `json:"reason"`
}

func NewEvent(id, eventType string, payload any) (Event, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s payload: %w", eventType, err)
	}

	return Event{
		ID:         id,
		Type:       eventType,
		Version:    1,
		OccurredAt: time.Now().UTC(),
		Payload:    encoded,
	}, nil
}

func Decode[T any](event Event) (T, error) {
	var payload T

	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return payload, fmt.Errorf("decode %s: %w", event.Type, err)
	}

	return payload, nil
}

//
// STREAM SETUP
//

// EnsureStream creates or updates the stream that stores these events.
//
// The stream is the durable buffer: publishers write to a subject, the stream
// persists it, and consumers read at their own pace - which is what lets a
// worker be restarted, or start hours later, without losing anything.
func EnsureStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        StreamName,
		Description: "Order domain events",
		Subjects:    []string{SubjectAll},

		// File beats memory for anything that must survive a broker restart.
		Storage: jetstream.FileStorage,

		// Retention: keep messages until every consumer has acked them. The
		// alternative (LimitsPolicy) keeps them for a fixed age or size and
		// is what you want for replayable event logs.
		Retention: jetstream.WorkQueuePolicy,

		// Bounds. A stream with no limits fills the disk, and the failure
		// mode is the whole broker rather than one subject.
		MaxMsgs:  100_000,
		MaxBytes: 64 * 1024 * 1024,
		MaxAge:   24 * time.Hour,

		// Server-side deduplication: two publishes with the same Nats-Msg-Id
		// inside this window store one message. It removes duplicates caused
		// by a PUBLISHER retry - it does not remove redeliveries, so the
		// consumer still needs to be idempotent.
		Duplicates: 2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream %s: %w", StreamName, err)
	}

	return stream, nil
}

// EnsureDeadLetterStream creates the stream that holds messages no consumer
// could process.
//
// It must exist BEFORE any worker starts, or the first dead letter has
// nowhere to go and is lost - which is the one message you most wanted to
// keep.
func EnsureDeadLetterStream(ctx context.Context, js jetstream.JetStream) (jetstream.Stream, error) {
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        DeadLetterStream,
		Description: "Messages that exhausted their retries",
		Subjects:    []string{"dlq.>"},
		Storage:     jetstream.FileStorage,
		// Keep them long enough for somebody to notice and investigate.
		MaxAge: 7 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream %s: %w", DeadLetterStream, err)
	}

	return stream, nil
}

//
// PUBLISHER
//

type Publisher struct {
	js jetstream.JetStream
}

func NewPublisher(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// Publish sends an event and WAITS for the broker's acknowledgment.
//
// The synchronous form is the right default: it is the difference between
// "the broker has it" and "we handed it to a socket". PublishAsync is faster
// and needs its own error handling for the acks that never arrive.
func (p *Publisher) Publish(ctx context.Context, subject string, event Event) (uint64, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return 0, fmt.Errorf("encode event: %w", err)
	}

	ack, err := p.js.Publish(ctx, subject, payload,
		// The message id drives the stream's deduplication window: a
		// publisher that retries after a timeout does not create a second
		// event.
		jetstream.WithMsgID(event.ID),
	)
	if err != nil {
		return 0, fmt.Errorf("publish %s: %w", subject, err)
	}

	return ack.Sequence, nil
}

//
// CONNECTION
//

// Connect dials NATS with the options a production client needs.
func Connect(url string) (*nats.Conn, jetstream.JetStream, error) {
	connection, err := nats.Connect(url,
		nats.Name("day83"),
		// Reconnect forever: a broker restart must not require a redeploy.
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.Timeout(5*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			// A disconnect is expected occasionally; it is worth a log line,
			// not a panic.
			_ = err
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to nats at %s: %w", url, err)
	}

	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()

		return nil, nil, fmt.Errorf("jetstream: %w", err)
	}

	return connection, js, nil
}
