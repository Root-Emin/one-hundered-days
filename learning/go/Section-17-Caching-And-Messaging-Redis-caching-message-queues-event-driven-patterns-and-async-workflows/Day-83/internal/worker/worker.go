// Package worker consumes the order events.
//
// A worker is a separate process from the API in production: it scales on
// queue depth rather than on request rate, and a slow consumer must never
// slow a request down.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-83/internal/events"
)

// ErrPoison marks a message that will never succeed. It is terminated rather
// than retried, so the retry budget is spent on failures that might pass.
var ErrPoison = errors.New("message cannot be processed")

// Handler processes one decoded event.
type Handler func(ctx context.Context, event events.Event) error

type Config struct {
	// Durable names the consumer. A DURABLE consumer's position survives a
	// restart: the worker picks up where it left off. Without a name, the
	// consumer is ephemeral and a restart re-reads or skips.
	Durable string

	// FilterSubject narrows what this consumer sees.
	FilterSubject string

	// MaxDeliver bounds redelivery. Past it, the message is dead-lettered.
	MaxDeliver int

	// AckWait is how long the worker has before the server assumes it died.
	// It must exceed the slowest legitimate handler run.
	AckWait time.Duration

	// MaxAckPending bounds in-flight messages: the consumer's backpressure.
	MaxAckPending int

	// BackOff is the delay before each redelivery. A per-attempt schedule
	// beats one fixed delay: fast for a blip, slow for an outage.
	BackOff []time.Duration

	// DeadLetterSubject receives messages that exhausted MaxDeliver.
	DeadLetterSubject string
}

func DefaultConfig(durable string) Config {
	return Config{
		Durable:           durable,
		FilterSubject:     events.SubjectAll,
		MaxDeliver:        4,
		AckWait:           5 * time.Second,
		MaxAckPending:     64,
		BackOff:           []time.Duration{200 * time.Millisecond, time.Second, 3 * time.Second},
		DeadLetterSubject: events.SubjectDeadLetter,
	}
}

type Worker struct {
	js      jetstream.JetStream
	config  Config
	logger  *slog.Logger
	handler Handler

	processed atomic.Int64
	retried   atomic.Int64
	dead      atomic.Int64

	mu       sync.Mutex
	consumer jetstream.Consumer
}

func New(js jetstream.JetStream, config Config, logger *slog.Logger, handler Handler) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	return &Worker{js: js, config: config, logger: logger, handler: handler}
}

func (w *Worker) Counts() (processed, retried, dead int64) {
	return w.processed.Load(), w.retried.Load(), w.dead.Load()
}

// Start creates the consumer and begins processing. The returned stop
// function drains in-flight work before returning.
func (w *Worker) Start(ctx context.Context) (func(), error) {
	consumer, err := w.js.CreateOrUpdateConsumer(ctx, events.StreamName, jetstream.ConsumerConfig{
		Durable:       w.config.Durable,
		FilterSubject: w.config.FilterSubject,

		// Explicit acks: the server waits for the worker to say it is done.
		// AckNone would be at-most-once, and a crash would lose work.
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       w.config.AckWait,
		MaxDeliver:    w.config.MaxDeliver,
		BackOff:       w.config.BackOff,
		MaxAckPending: w.config.MaxAckPending,

		// Start from the beginning of the stream: a new consumer processes
		// the backlog rather than skipping it.
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", w.config.Durable, err)
	}

	w.mu.Lock()
	w.consumer = consumer
	w.mu.Unlock()

	// Consume delivers messages to the callback until stopped. Several
	// workers with the SAME durable name share the stream between them,
	// which is how this scales horizontally.
	subscription, err := consumer.Consume(func(message jetstream.Msg) {
		w.process(ctx, message)
	})
	if err != nil {
		return nil, fmt.Errorf("consume: %w", err)
	}

	return subscription.Stop, nil
}

func (w *Worker) process(ctx context.Context, message jetstream.Msg) {
	metadata, err := message.Metadata()

	deliveries := uint64(1)

	if err == nil {
		deliveries = metadata.NumDelivered
	}

	var event events.Event

	if err := json.Unmarshal(message.Data(), &event); err != nil {
		// Malformed JSON will be malformed on every redelivery: terminate it.
		w.logger.Error("undecodable message",
			slog.String("subject", message.Subject()),
			slog.String("error", err.Error()))

		w.terminate(message, event, "decode failed")

		return
	}

	logger := w.logger.With(
		slog.String("event_id", event.ID),
		slog.String("type", event.Type),
		slog.Uint64("delivery", deliveries),
	)

	err = w.handler(ctx, event)

	switch {
	case err == nil:
		if ackErr := message.Ack(); ackErr != nil {
			// A failed ack means the message WILL be redelivered - which is
			// exactly why the handler has to be idempotent.
			logger.Error("ack failed", slog.String("error", ackErr.Error()))
		}

		w.processed.Add(1)

	case errors.Is(err, ErrPoison):
		logger.Warn("poison message", slog.String("error", err.Error()))

		w.terminate(message, event, err.Error())

	case deliveries >= uint64(w.config.MaxDeliver):
		// The last attempt failed: this message is about to be dropped by the
		// server, so copy it somewhere a human can look at it.
		logger.Error("giving up", slog.String("error", err.Error()))

		w.terminate(message, event, err.Error())

	default:
		logger.Warn("retrying", slog.String("error", err.Error()))

		w.retried.Add(1)

		// Nak with a delay: the server redelivers after it, instead of
		// immediately hammering a dependency that just failed.
		if nakErr := message.NakWithDelay(w.backoffFor(deliveries)); nakErr != nil {
			logger.Error("nak failed", slog.String("error", nakErr.Error()))
		}
	}
}

func (w *Worker) backoffFor(delivery uint64) time.Duration {
	if len(w.config.BackOff) == 0 {
		return time.Second
	}

	index := int(delivery) - 1

	if index >= len(w.config.BackOff) {
		index = len(w.config.BackOff) - 1
	}

	return w.config.BackOff[index]
}

// terminate stops redelivery and copies the message to the dead letter
// subject, where it can be inspected and replayed once the bug is fixed.
func (w *Worker) terminate(message jetstream.Msg, event events.Event, reason string) {
	w.dead.Add(1)

	if w.config.DeadLetterSubject != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		headers := map[string][]string{
			"Original-Subject": {message.Subject()},
			"Failure-Reason":   {reason},
		}

		if _, err := w.js.PublishMsg(ctx, &nats.Msg{
			Subject: w.config.DeadLetterSubject,
			Data:    message.Data(),
			Header:  headers,
		}); err != nil {
			w.logger.Error("dead letter publish failed",
				slog.String("event_id", event.ID), slog.String("error", err.Error()))
		}
	}

	if err := message.Term(); err != nil {
		w.logger.Error("term failed", slog.String("error", err.Error()))
	}
}

// Lag reports how far behind this consumer is.
//
// Pending is THE number to alert on: it is the queue depth the worker has not
// caught up with, and a steadily rising value means the consumer is slower
// than the producer.
type Lag struct {
	Pending       uint64
	AckPending    int
	Redelivered   int
	StreamLastSeq uint64
	ConsumerSeq   uint64
}

func (w *Worker) Lag(ctx context.Context) (Lag, error) {
	w.mu.Lock()
	consumer := w.consumer
	w.mu.Unlock()

	if consumer == nil {
		return Lag{}, errors.New("worker has not started")
	}

	info, err := consumer.Info(ctx)
	if err != nil {
		return Lag{}, fmt.Errorf("consumer info: %w", err)
	}

	stream, err := w.js.Stream(ctx, events.StreamName)
	if err != nil {
		return Lag{}, fmt.Errorf("stream: %w", err)
	}

	streamInfo, err := stream.Info(ctx)
	if err != nil {
		return Lag{}, fmt.Errorf("stream info: %w", err)
	}

	return Lag{
		Pending:       info.NumPending,
		AckPending:    info.NumAckPending,
		Redelivered:   info.NumRedelivered,
		StreamLastSeq: streamInfo.State.LastSeq,
		ConsumerSeq:   info.Delivered.Stream,
	}, nil
}
