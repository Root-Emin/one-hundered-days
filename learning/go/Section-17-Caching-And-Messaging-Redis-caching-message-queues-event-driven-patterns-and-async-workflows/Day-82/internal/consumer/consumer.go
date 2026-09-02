// Package consumer holds the piece every at-least-once system needs: a
// handler that is safe to run twice.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-82/internal/broker"
)

// Deduplicator remembers which messages have been processed.
//
// In production this is a table with a unique index on the dedup key, or a
// Redis SET with a TTL - the important property being that the CHECK and the
// WORK happen in one transaction, or the check is worthless.
type Deduplicator struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func NewDeduplicator(ttl time.Duration) *Deduplicator {
	if ttl <= 0 {
		ttl = time.Hour
	}

	return &Deduplicator{seen: make(map[string]time.Time), ttl: ttl}
}

// Claim records the key and reports whether this caller is the first.
//
// The TTL is a trade: remembering forever is unbounded storage, and
// remembering for an hour means a redelivery after two hours is processed
// twice. Size it against the broker's maximum redelivery window.
func (d *Deduplicator) Claim(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	if at, found := d.seen[key]; found && now.Sub(at) < d.ttl {
		return false
	}

	d.seen[key] = now

	return true
}

// Release removes a claim, so a FAILED attempt can be retried.
//
// Without this, a message that fails after claiming its key can never be
// retried - the dedup store says it was already handled.
func (d *Deduplicator) Release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.seen, key)
}

func (d *Deduplicator) Size() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.seen)
}

//
// AN IDEMPOTENT CONSUMER
//

var ErrPoison = errors.New("message cannot be processed")

// EmailSender is the side effect being protected: sending the same welcome
// email three times is exactly the failure at-least-once delivery causes.
type EmailSender struct {
	mu   sync.Mutex
	sent []string

	failuresBeforeSuccess int
	attempts              atomic.Int64
}

func NewEmailSender(failuresBeforeSuccess int) *EmailSender {
	return &EmailSender{failuresBeforeSuccess: failuresBeforeSuccess}
}

func (s *EmailSender) Send(ctx context.Context, address string) error {
	attempt := s.attempts.Add(1)

	if int(attempt) <= s.failuresBeforeSuccess {
		return fmt.Errorf("smtp temporarily unavailable (attempt %d)", attempt)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sent = append(s.sent, address)

	return nil
}

func (s *EmailSender) Sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.sent...)
}

func (s *EmailSender) Attempts() int64 { return s.attempts.Load() }

// WelcomeHandler is the shape every consumer should have.
type WelcomeHandler struct {
	deduplicator *Deduplicator
	sender       *EmailSender

	processed atomic.Int64
	skipped   atomic.Int64
	failed    atomic.Int64
}

func NewWelcomeHandler(deduplicator *Deduplicator, sender *EmailSender) *WelcomeHandler {
	return &WelcomeHandler{deduplicator: deduplicator, sender: sender}
}

func (h *WelcomeHandler) Counts() (processed, skipped, failed int64) {
	return h.processed.Load(), h.skipped.Load(), h.failed.Load()
}

// Handle is the pattern, in order:
//
//  1. derive a stable dedup key from the MESSAGE, not from the delivery
//  2. claim it; if it was already claimed, ack and stop
//  3. do the work
//  4. on failure, release the claim and nack, so a retry can happen
//  5. ack only after the work succeeded
func (h *WelcomeHandler) Handle(ctx context.Context, message *broker.Message) {
	// The key must identify the EVENT, not the delivery: message.ID changes
	// per publish, so a header the producer sets (an event id) is what makes
	// deduplication work across publishers too.
	key := message.Headers["event-id"]

	if key == "" {
		key = message.ID
	}

	if !h.deduplicator.Claim(key) {
		// Already handled. Ack, because redelivering it again helps nobody.
		h.skipped.Add(1)
		message.Ack()

		return
	}

	address := string(message.Payload)

	if address == "" {
		// Poison: it will fail identically every time. Terminate rather than
		// retry, so the dead letter queue gets it immediately.
		h.failed.Add(1)
		h.deduplicator.Release(key)
		message.Term()

		return
	}

	if err := h.sender.Send(ctx, address); err != nil {
		h.failed.Add(1)

		// Release the claim: this attempt did not do the work, so a retry
		// must be allowed to.
		h.deduplicator.Release(key)
		message.Nack()

		return
	}

	h.processed.Add(1)
	message.Ack()
}
