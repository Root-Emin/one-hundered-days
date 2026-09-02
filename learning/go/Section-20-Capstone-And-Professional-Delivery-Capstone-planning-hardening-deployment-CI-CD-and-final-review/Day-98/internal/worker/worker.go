// Package worker drains the outbox into the daily aggregate.
//
// It is the async half of ADR 0003: the redirect writes an event and returns,
// and this turns events into the numbers the stats endpoint reads.
//
// Three properties it has to have, and the reasons:
//
//	idempotent   at-least-once delivery means a redelivery is normal, so
//	             applying the same event twice must not double a count
//	bounded      one batch at a time, so a backlog does not become one enormous
//	             transaction that blocks every writer
//	stoppable    it exits when its context is cancelled, or a shutdown waits
//	             for a poll interval that may be minutes
package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/store"
)

// Repository is the persistence the worker needs.
type Repository interface {
	UnpublishedEvents(ctx context.Context, limit int) ([]store.Event, error)
	ApplyClickBatches(ctx context.Context, batches []store.ClickBatch) (int, error)
	RecordEventFailure(ctx context.Context, id int64) error
	PendingEvents(ctx context.Context) (int, error)
}

// Observer receives the queue depth after each pass, for metrics.
type Observer interface {
	SetOutboxPending(count int)
}

// Worker aggregates click events.
type Worker struct {
	repo     Repository
	logger   *slog.Logger
	observer Observer

	interval  time.Duration
	batchSize int

	applied    atomic.Int64
	duplicates atomic.Int64
	failed     atomic.Int64
}

// Options configure a Worker.
type Options struct {
	// Interval is how often the outbox is polled.
	Interval time.Duration
	// BatchSize bounds one pass.
	BatchSize int
	// Observer publishes the queue depth; may be nil.
	Observer Observer
}

// New builds a Worker.
func New(repo Repository, logger *slog.Logger, options Options) *Worker {
	if logger == nil {
		logger = slog.Default()
	}

	if options.Interval <= 0 {
		options.Interval = time.Second
	}

	if options.BatchSize <= 0 {
		options.BatchSize = 200
	}

	return &Worker{
		repo:      repo,
		logger:    logger,
		observer:  options.Observer,
		interval:  options.Interval,
		batchSize: options.BatchSize,
	}
}

// Run polls until the context is cancelled.
//
// It returns only after the current pass finishes, so a shutdown never leaves
// a transaction half-open.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("click worker started", slog.Duration("interval", w.interval))

	for {
		select {
		case <-ctx.Done():
			applied, duplicates, failed := w.Stats()

			w.logger.Info("click worker stopped",
				slog.Int64("applied", applied),
				slog.Int64("duplicates", duplicates),
				slog.Int64("failed", failed))

			return

		case <-ticker.C:
			if err := w.Drain(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("drain outbox", slog.String("error", err.Error()))
			}
		}
	}
}

// maxPassesPerTick bounds one Run tick.
//
// The worker keeps draining while there is a backlog rather than doing one
// batch per second - which is how it fell 1,792 events behind in the load
// test - but it does not loop forever: a tick that never ends is a shutdown
// that never happens.
const maxPassesPerTick = 20

// Drain processes the backlog. Exported so a test - or a shutdown - can flush
// without waiting for a tick.
func (w *Worker) Drain(ctx context.Context) error {
	for pass := 0; pass < maxPassesPerTick; pass++ {
		events, err := w.repo.UnpublishedEvents(ctx, w.batchSize)
		if err != nil {
			return err
		}

		if len(events) == 0 {
			break
		}

		if err := w.applyBatch(ctx, events); err != nil {
			return err
		}

		if len(events) < w.batchSize {
			// A short read means the queue is empty; another pass would be a
			// query for nothing.
			break
		}

		if ctx.Err() != nil {
			break
		}
	}

	if w.observer != nil {
		pending, err := w.repo.PendingEvents(ctx)
		if err != nil {
			return err
		}

		w.observer.SetOutboxPending(pending)
	}

	return nil
}

// applyBatch groups events by (code, day) and applies them in one transaction.
//
// A counter increment is associative: +1 five thousand times is +5000 once.
// That is what turns a per-click transaction - which SQLite serialises - into
// a single write.
func (w *Worker) applyBatch(ctx context.Context, events []store.Event) error {
	type key struct{ code, day string }

	grouped := make(map[key]*store.ClickBatch)

	order := make([]key, 0, len(events))

	for _, event := range events {
		var payload struct {
			Code string `json:"code"`
			Day  string `json:"day"`
		}

		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			// A payload that cannot be parsed will never parse. Retrying is
			// pointless and would block the queue behind it, so it is counted
			// and left for a human - the attempts column makes it findable.
			w.failed.Add(1)

			if recordErr := w.repo.RecordEventFailure(ctx, event.ID); recordErr != nil {
				return recordErr
			}

			w.logger.Error("undecodable click event",
				slog.String("event_id", event.EventID), slog.String("error", err.Error()))

			continue
		}

		identity := key{code: payload.Code, day: payload.Day}

		batch, found := grouped[identity]
		if !found {
			grouped[identity] = &store.ClickBatch{Code: payload.Code, Day: payload.Day}
			batch = grouped[identity]

			order = append(order, identity)
		}

		batch.Count++
		batch.EventIDs = append(batch.EventIDs, event.ID)
	}

	batches := make([]store.ClickBatch, 0, len(order))

	for _, identity := range order {
		batches = append(batches, *grouped[identity])
	}

	applied, err := w.repo.ApplyClickBatches(ctx, batches)
	if err != nil {
		w.failed.Add(int64(len(events)))

		return err
	}

	w.applied.Add(int64(applied))

	// Events another worker published first are counted as duplicates rather
	// than lost: at-least-once delivery makes that the expected outcome, not
	// an error.
	if duplicates := countEvents(batches) - applied; duplicates > 0 {
		w.duplicates.Add(int64(duplicates))
	}

	return nil
}

func countEvents(batches []store.ClickBatch) int {
	total := 0

	for _, batch := range batches {
		total += len(batch.EventIDs)
	}

	return total
}

// Stats reports what the worker has done.
func (w *Worker) Stats() (applied, duplicates, failed int64) {
	return w.applied.Load(), w.duplicates.Load(), w.failed.Load()
}
