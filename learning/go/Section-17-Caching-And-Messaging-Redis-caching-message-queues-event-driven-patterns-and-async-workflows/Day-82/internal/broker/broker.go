// Package broker is a small in-process message broker.
//
// It is not a production broker. It exists so the CONCEPTS - producer,
// consumer, topic, acknowledgment, redelivery, dead letter - can be read,
// run and tested in one file, before meeting them through someone else's
// client library.
//
// Every behaviour here has a real counterpart:
//
//	this                     NATS JetStream    RabbitMQ         Kafka
//	Publish                  js.Publish        basic.publish    Produce
//	Subscribe(queue group)   consumer          queue+consumer   consumer group
//	Ack                      msg.Ack()         basic.ack        commit offset
//	Nack                     msg.Nak()         basic.nack       (seek back)
//	AckWait redelivery       ack_wait          consumer timeout (poll timeout)
//	Dead letter              max_deliver+DLQ   DLX              a DLQ topic
package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed       = errors.New("broker is closed")
	ErrNoSubscriber = errors.New("no subscriber for topic")
)

// Message is what travels. The fields are the ones every broker has in some
// form, and each exists for a reason a comment can state.
type Message struct {
	// ID is the broker's identifier for this delivery attempt's message.
	ID string

	// Topic is the named channel. Producers publish to it; consumers
	// subscribe to it.
	Topic string

	// Key groups related messages. In Kafka it decides the partition, and
	// therefore the ORDER guarantee: messages with the same key are ordered
	// relative to each other, and nothing else is.
	Key string

	Payload []byte

	// Headers carry metadata that is not part of the payload: a trace id, a
	// content type, a schema version.
	Headers map[string]string

	// Deliveries counts how many times this message has been handed to a
	// consumer. The first delivery is 1. A consumer that sees > 1 knows it is
	// looking at a redelivery - which is exactly when idempotency matters.
	Deliveries int

	PublishedAt time.Time

	ackOnce sync.Once
	ack     chan ackResult
}

type ackResult struct {
	acknowledged bool
	requeue      bool
}

// Ack tells the broker the message was processed and must not be redelivered.
//
// Ack AFTER the work, never before: acking first turns at-least-once into
// at-most-once, and a crash between the ack and the work loses the message.
func (m *Message) Ack() {
	m.settle(ackResult{acknowledged: true})
}

// Nack says processing failed and the message should be redelivered.
func (m *Message) Nack() {
	m.settle(ackResult{requeue: true})
}

// Term says the message is poison: do not redeliver, send it to the dead
// letter topic. Use it for a message that will fail every time - malformed
// JSON, a reference to something deleted - rather than burning retries on it.
func (m *Message) Term() {
	m.settle(ackResult{})
}

// settle records the outcome exactly once.
//
// A message constructed directly - in a test, or by a handler being exercised
// without a broker - has no ack channel. Settling it must be a no-op rather
// than a deadlock: a handler should be callable on its own.
func (m *Message) settle(result ackResult) {
	m.ackOnce.Do(func() {
		if m.ack == nil {
			return
		}

		m.ack <- result
	})
}

type Handler func(ctx context.Context, message *Message)

type Config struct {
	// AckWait is how long a consumer has before the broker assumes it died
	// and redelivers. Too short and slow work is processed twice; too long
	// and a crashed consumer's messages sit idle.
	AckWait time.Duration

	// MaxDeliveries bounds redelivery. Without it, a poison message is
	// retried forever and can consume an entire consumer.
	MaxDeliveries int

	// DeadLetterTopic receives messages that exceeded MaxDeliveries.
	DeadLetterTopic string

	// Buffer is the per-topic queue depth. A full queue means the producer
	// blocks, which is backpressure - and better than unbounded memory.
	Buffer int
}

func DefaultConfig() Config {
	return Config{
		AckWait:         2 * time.Second,
		MaxDeliveries:   3,
		DeadLetterTopic: "dead-letter",
		Buffer:          128,
	}
}

type Broker struct {
	config Config

	mu     sync.RWMutex
	topics map[string]*topic
	closed bool

	published  atomic.Int64
	delivered  atomic.Int64
	acked      atomic.Int64
	requeued   atomic.Int64
	deadLetter atomic.Int64

	wait sync.WaitGroup
}

type topic struct {
	name        string
	messages    chan *Message
	dispatching bool

	// subscribers in the same QUEUE GROUP share the messages: each message
	// goes to exactly one of them. That is a work queue.
	//
	// Subscribers in DIFFERENT groups each get a copy. That is pub/sub.
	groups map[string]*queueGroup
}

type queueGroup struct {
	name     string
	handlers chan *Message
	count    int
}

func New(config Config) *Broker {
	if config.AckWait <= 0 {
		config = DefaultConfig()
	}

	return &Broker{config: config, topics: make(map[string]*topic)}
}

func (b *Broker) topicFor(name string) *topic {
	b.mu.Lock()
	defer b.mu.Unlock()

	existing, found := b.topics[name]
	if found {
		return existing
	}

	created := &topic{
		name:     name,
		messages: make(chan *Message, b.config.Buffer),
		groups:   make(map[string]*queueGroup),
	}

	b.topics[name] = created

	return created
}

// Publish sends a message. The producer does not know or care whether anyone
// is subscribed - that decoupling is the point of a broker.
func (b *Broker) Publish(ctx context.Context, topicName, key string, payload []byte, headers map[string]string) (string, error) {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()

	if closed {
		return "", ErrClosed
	}

	message := &Message{
		ID:          fmt.Sprintf("msg-%d", b.published.Add(1)),
		Topic:       topicName,
		Key:         key,
		Payload:     payload,
		Headers:     headers,
		PublishedAt: time.Now(),
	}

	destination := b.topicFor(topicName)

	select {
	case destination.messages <- message:
		return message.ID, nil

	case <-ctx.Done():
		// A full buffer blocking the producer IS backpressure. Failing here
		// beats growing an unbounded queue until the process dies.
		return "", fmt.Errorf("publish to %s: %w", topicName, ctx.Err())
	}
}

// Subscribe starts a consumer.
//
// Consumers in the same group share the work; different groups each receive
// every message.
func (b *Broker) Subscribe(ctx context.Context, topicName, group string, handler Handler) {
	destination := b.topicFor(topicName)

	b.mu.Lock()

	// One dispatcher per TOPIC, started with the first subscriber to it. One
	// per group would be wrong: every dispatcher would compete for the same
	// topic channel, and each message would reach exactly one group instead
	// of all of them.
	if !destination.dispatching {
		destination.dispatching = true

		b.wait.Add(1)

		go b.dispatch(ctx, destination)
	}

	consumerGroup, found := destination.groups[group]
	if !found {
		consumerGroup = &queueGroup{name: group, handlers: make(chan *Message, b.config.Buffer)}
		destination.groups[group] = consumerGroup
	}

	consumerGroup.count++

	b.mu.Unlock()

	b.wait.Add(1)

	go b.consume(ctx, consumerGroup, handler)
}

// dispatch fans one topic's messages out to EVERY group.
//
// Each group gets its own copy, because acknowledgment is per group: the
// email consumer acking a message must not affect the analytics consumer's
// delivery of the same event.
func (b *Broker) dispatch(ctx context.Context, destination *topic) {
	defer b.wait.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case message, ok := <-destination.messages:
			if !ok {
				return
			}

			// Read the group set fresh each time: a subscriber added after
			// the dispatcher started must still receive messages.
			b.mu.RLock()

			groups := make([]*queueGroup, 0, len(destination.groups))

			for _, group := range destination.groups {
				groups = append(groups, group)
			}

			b.mu.RUnlock()

			for _, group := range groups {
				copied := message.copy()

				select {
				case group.handlers <- copied:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// copy produces an independent delivery of the same message.
func (m *Message) copy() *Message {
	return &Message{
		ID:          m.ID,
		Topic:       m.Topic,
		Key:         m.Key,
		Payload:     m.Payload,
		Headers:     m.Headers,
		Deliveries:  m.Deliveries,
		PublishedAt: m.PublishedAt,
	}
}

// consume runs one consumer: take a message, hand it to the handler, wait for
// an ack, and redeliver if none arrives in time.
func (b *Broker) consume(ctx context.Context, group *queueGroup, handler Handler) {
	defer b.wait.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case message, ok := <-group.handlers:
			if !ok {
				return
			}

			b.deliver(ctx, group, message, handler)
		}
	}
}

func (b *Broker) deliver(ctx context.Context, group *queueGroup, message *Message, handler Handler) {
	message.Deliveries++
	message.ackOnce = sync.Once{}
	message.ack = make(chan ackResult, 1)

	b.delivered.Add(1)

	done := make(chan struct{})

	go func() {
		defer close(done)

		defer func() {
			// A panicking handler must not kill the consumer: treat it as a
			// nack so the message is retried or dead-lettered.
			if recovered := recover(); recovered != nil {
				message.Nack()
			}
		}()

		handler(ctx, message)
	}()

	var result ackResult

	select {
	case result = <-message.ack:
	case <-time.After(b.config.AckWait):
		// No ack in time: assume the consumer died. This is precisely why
		// delivery is AT LEAST ONCE - the message may well have been
		// processed, and we are about to deliver it again.
		result = ackResult{requeue: true}

	case <-ctx.Done():
		return
	}

	switch {
	case result.acknowledged:
		b.acked.Add(1)

	case result.requeue && message.Deliveries < b.config.MaxDeliveries:
		b.requeued.Add(1)

		// Requeue on a separate goroutine so a full buffer cannot deadlock
		// the consumer that is trying to requeue into it.
		go func() {
			select {
			case group.handlers <- message:
			case <-ctx.Done():
			}
		}()

	default:
		// Either terminated, or out of attempts: dead-letter it.
		b.deadLetter.Add(1)

		if b.config.DeadLetterTopic != "" && message.Topic != b.config.DeadLetterTopic {
			headers := map[string]string{
				"original-topic":   message.Topic,
				"original-id":      message.ID,
				"delivery-count":   fmt.Sprint(message.Deliveries),
				"dead-lettered-at": time.Now().Format(time.RFC3339),
			}

			for key, value := range message.Headers {
				headers[key] = value
			}

			publishCtx, cancel := context.WithTimeout(context.Background(), time.Second)

			if _, err := b.Publish(publishCtx, b.config.DeadLetterTopic, message.Key, message.Payload, headers); err != nil {
				// Nothing left to do but count it: the message is lost.
				_ = err
			}

			cancel()
		}
	}

	<-done
}

type Stats struct {
	Published  int64
	Delivered  int64
	Acked      int64
	Requeued   int64
	DeadLetter int64
}

func (b *Broker) Stats() Stats {
	return Stats{
		Published:  b.published.Load(),
		Delivered:  b.delivered.Load(),
		Acked:      b.acked.Load(),
		Requeued:   b.requeued.Load(),
		DeadLetter: b.deadLetter.Load(),
	}
}

// Pending reports messages waiting to be delivered - the local equivalent of
// consumer lag, and the number to alert on.
func (b *Broker) Pending(topicName string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	destination, found := b.topics[topicName]
	if !found {
		return 0
	}

	pending := len(destination.messages)

	for _, group := range destination.groups {
		pending += len(group.handlers)
	}

	return pending
}

func (b *Broker) Close() error {
	b.mu.Lock()

	if b.closed {
		b.mu.Unlock()

		return nil
	}

	b.closed = true
	b.mu.Unlock()

	return nil
}
