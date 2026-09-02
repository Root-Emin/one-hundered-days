// Package orders is the service under trace: an HTTP handler, a repository
// call, and an outbound call to the payments service.
package orders

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

type Order struct {
	ID       int64
	Customer string
	Cents    int64
	Status   string
}

// Repository is instrumented because "the database was slow" is the most
// common answer a trace gives.
type Repository struct {
	tracer trace.Tracer

	mu     sync.Mutex
	orders map[int64]Order
	nextID int64
}

func NewRepository() *Repository {
	return &Repository{
		tracer: tracing.Tracer("orders/repository"),
		orders: make(map[int64]Order),
		nextID: 1,
	}
}

func (r *Repository) Save(ctx context.Context, order Order) (Order, error) {
	// A span per query, named after the operation. Semantic conventions ask
	// for db.system and db.operation so a viewer can group them.
	ctx, span := r.tracer.Start(ctx, "repository.Save",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "sqlite"),
			attribute.String("db.operation", "INSERT"),
			attribute.String("db.sql.table", "orders"),
		))
	defer span.End()

	//nolint:gosec // load shaping, not security
	delay := time.Duration(2+rand.IntN(8)) * time.Millisecond

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		tracing.RecordError(span, ctx.Err())

		return Order{}, ctx.Err()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	order.ID = r.nextID
	order.Status = "pending"
	r.orders[order.ID] = order
	r.nextID++

	// The id is worth putting on the span: it is what an engineer searches by.
	span.SetAttributes(attribute.Int64("order.id", order.ID))

	return order, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string) error {
	ctx, span := r.tracer.Start(ctx, "repository.UpdateStatus",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "sqlite"),
			attribute.String("db.operation", "UPDATE"),
			attribute.Int64("order.id", id),
			attribute.String("order.status", status),
		))
	defer span.End()

	select {
	case <-time.After(2 * time.Millisecond):
	case <-ctx.Done():
		tracing.RecordError(span, ctx.Err())

		return ctx.Err()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	order, found := r.orders[id]
	if !found {
		err := fmt.Errorf("order %d not found", id)

		tracing.RecordError(span, err)

		return err
	}

	order.Status = status
	r.orders[id] = order

	return nil
}

func (r *Repository) ByID(id int64) (Order, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	order, found := r.orders[id]

	return order, found
}
