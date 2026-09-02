// Package idempotency makes a consumer safe to run twice.
//
// At-least-once delivery is not a corner case: a redelivery happens on every
// consumer restart, every ack timeout, every relay crash. The handler must
// produce the same outcome the second time.
//
// The mechanism is a table with a unique index, and the crucial detail is that
// the CLAIM and the WORK share one transaction. Checking "have I seen this?"
// in one transaction and doing the work in another leaves a window where two
// deliveries both pass the check.
package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const Schema = `
CREATE TABLE IF NOT EXISTS processed_events (
	event_id     TEXT PRIMARY KEY,
	consumer     TEXT NOT NULL,
	processed_at TEXT NOT NULL DEFAULT (datetime('now')),
	result       TEXT
);

-- Old rows are pruned by age, so the table does not grow forever. The
-- retention must exceed the broker's maximum redelivery window, or a late
-- redelivery is processed a second time.
CREATE INDEX IF NOT EXISTS idx_processed_at ON processed_events (processed_at);`

// ErrAlreadyProcessed means this event was handled before. It is not a
// failure: the caller acks and moves on.
var ErrAlreadyProcessed = errors.New("event already processed")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Handler does the real work, inside the transaction that claims the event.
type Handler func(ctx context.Context, tx *sql.Tx) (string, error)

// Process runs the handler exactly once per (consumer, event) pair.
//
// The sequence:
//
//  1. begin
//  2. INSERT the claim - the unique index is what makes this atomic
//  3. run the handler in the same transaction
//  4. commit: the claim and the work land together
//
// If the handler fails, the rollback removes the claim too, so a retry can
// happen. If the process dies mid-transaction, the database rolls back for us.
func (s *Store) Process(ctx context.Context, consumer, eventID string, handler Handler) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	committed := false

	defer func() {
		if committed {
			return
		}

		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = err
		}
	}()

	// The claim. A second delivery arriving concurrently hits the unique
	// index and loses - which is the whole mechanism.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO processed_events (event_id, consumer) VALUES (?, ?);`,
		claimKey(consumer, eventID), consumer)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%s: %w", eventID, ErrAlreadyProcessed)
		}

		return fmt.Errorf("claim %s: %w", eventID, err)
	}

	result, err := handler(ctx, tx)
	if err != nil {
		// The rollback below removes the claim, so a retry is possible.
		return fmt.Errorf("handle %s: %w", eventID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE processed_events SET result = ? WHERE event_id = ?;`,
		result, claimKey(consumer, eventID)); err != nil {
		return fmt.Errorf("record result: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	committed = true

	return nil
}

// claimKey scopes the claim to one consumer.
//
// Two different consumers must each process the event once: the email service
// and the analytics service both care about order.created. Without the
// consumer in the key, whichever arrived first would silence the other.
func claimKey(consumer, eventID string) string {
	return consumer + ":" + eventID
}

func (s *Store) WasProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	var count int

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM processed_events WHERE event_id = ?;`,
		claimKey(consumer, eventID)).Scan(&count); err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}

	return count > 0, nil
}

// Prune removes claims older than the retention window.
//
// Sizing it: longer than the broker's maximum redelivery age, short enough
// that the table stays small. Days, usually.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.DateTime)

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM processed_events WHERE processed_at < ?;`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune: %w", err)
	}

	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune: rows affected: %w", err)
	}

	return removed, nil
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var count int

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM processed_events;`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}

	return count, nil
}

func isUniqueViolation(err error) bool {
	message := strings.ToUpper(err.Error())

	return strings.Contains(message, "UNIQUE CONSTRAINT FAILED") ||
		strings.Contains(message, "SQLSTATE 23505") ||
		strings.Contains(message, "DUPLICATE KEY")
}
