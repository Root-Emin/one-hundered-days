// Package orders is the instrumented, resilient service layer of the MVP.
package orders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/observability"
	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-75/internal/resilience"
)

var (
	ErrValidation = errors.New("invalid order")
	ErrNotFound   = errors.New("order not found")
	ErrDependency = errors.New("dependency unavailable")
)

type Order struct {
	ID       int64
	Customer string
	Cents    int64
	Status   string
}

// Chaos controls injected failure, so the telemetry has something to show.
type Chaos struct {
	DatabaseFailureRate atomic.Uint64 // percent, 0-100
	PaymentFailureRate  atomic.Uint64
	SlowRate            atomic.Uint64
}

type Service struct {
	observability *observability.Observability
	breaker       *resilience.Breaker
	retryPolicy   resilience.RetryPolicy

	Chaos Chaos

	mu     sync.Mutex
	orders map[int64]Order
	nextID int64
}

func NewService(obs *observability.Observability) *Service {
	service := &Service{
		observability: obs,
		orders:        make(map[int64]Order),
		nextID:        1,
	}

	service.breaker = resilience.NewBreaker(resilience.BreakerConfig{
		Name:            "payments",
		MinimumRequests: 5,
		FailureRatio:    0.5,
		Window:          10 * time.Second,
		// Short for a demo. In production this is tuned to how long the
		// dependency typically needs: too short and the probes become the
		// load that keeps it down, too long and a recovered service stays
		// unreachable.
		Cooldown:       3 * time.Second,
		HalfOpenProbes: 1,
		OnStateChange: func(name string, from, to resilience.State) {
			// A breaker that changes state silently is a mystery outage:
			// log it AND expose it as a metric.
			obs.Logger.Warn("circuit breaker state change",
				slog.String("dependency", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()))

			obs.Metrics.BreakerState.WithLabelValues(name).Set(stateValue(to))
		},
	})

	// Initialise every series this service will ever emit, at zero.
	//
	// A CounterVec has no time series until its first Inc, so a dashboard
	// panel for "retries" reads "no data" right up until the incident that
	// makes it matter - and "no data" and "zero" look very different at 3am.
	obs.Metrics.BreakerState.WithLabelValues("payments").Set(stateValue(resilience.StateClosed))
	obs.Metrics.RetriesTotal.WithLabelValues("payments").Add(0)

	for _, dependency := range []string{"payments", "database"} {
		for _, outcome := range []string{"success", "error", "breaker_open", "cancelled"} {
			obs.Metrics.DependencyCalls.WithLabelValues(dependency, outcome).Add(0)
		}
	}

	for _, outcome := range []string{"created", "rejected", "failed", "payment_failed"} {
		obs.Metrics.OrdersTotal.WithLabelValues(outcome).Add(0)
	}

	service.retryPolicy = resilience.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   20 * time.Millisecond,
		MaxDelay:    200 * time.Millisecond,
		Jitter:      true,
		OnRetry: func(attempt int, delay time.Duration, err error) {
			obs.Metrics.RetriesTotal.WithLabelValues("payments").Inc()
		},
	}

	return service
}

func stateValue(state resilience.State) float64 {
	switch state {
	case resilience.StateClosed:
		return 0
	case resilience.StateHalfOpen:
		return 1
	default:
		return 2
	}
}

func (s *Service) BreakerState() string { return s.breaker.State().String() }

// Create is the instrumented use case: a span, structured logs carrying the
// trace id, metrics for the outcome, and a resilient call to a dependency.
func (s *Service) Create(ctx context.Context, customer string, cents int64) (Order, error) {
	ctx, span := s.observability.Tracer.Start(ctx, "orders.Create",
		trace.WithAttributes(attribute.String("customer", customer)))
	defer span.End()

	logger := s.observability.Logger.With(
		slog.String("trace_id", observability.TraceID(ctx)),
		slog.String("customer", customer),
	)

	if customer == "" || cents <= 0 {
		err := fmt.Errorf("%w: customer and a positive amount are required", ErrValidation)

		observability.RecordError(span, err)
		s.observability.Metrics.OrdersTotal.WithLabelValues("rejected").Inc()

		return Order{}, err
	}

	order, err := s.persist(ctx, Order{Customer: customer, Cents: cents})
	if err != nil {
		observability.RecordError(span, err)
		logger.ErrorContext(ctx, "could not persist order", slog.String("error", err.Error()))
		s.observability.Metrics.OrdersTotal.WithLabelValues("failed").Inc()

		return Order{}, err
	}

	span.SetAttributes(attribute.Int64("order.id", order.ID))

	if err := s.charge(ctx, order); err != nil {
		observability.RecordError(span, err)

		logger.WarnContext(ctx, "payment failed",
			slog.Int64("order_id", order.ID),
			slog.String("error", err.Error()),
			slog.String("breaker", s.breaker.State().String()))

		s.observability.Metrics.OrdersTotal.WithLabelValues("payment_failed").Inc()

		s.setStatus(order.ID, "payment_failed")

		return Order{}, err
	}

	s.setStatus(order.ID, "paid")
	order.Status = "paid"

	s.observability.Metrics.OrdersTotal.WithLabelValues("created").Inc()

	logger.InfoContext(ctx, "order created", slog.Int64("order_id", order.ID))

	return order, nil
}

func (s *Service) persist(ctx context.Context, order Order) (Order, error) {
	ctx, span := s.observability.Tracer.Start(ctx, "repository.Save",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.operation", "INSERT")))
	defer span.End()

	start := time.Now()

	defer func() {
		s.observability.Metrics.DependencyDuration.
			WithLabelValues("database").Observe(time.Since(start).Seconds())
	}()

	if err := s.simulateLatency(ctx); err != nil {
		s.observability.Metrics.DependencyCalls.WithLabelValues("database", "cancelled").Inc()

		return Order{}, err
	}

	//nolint:gosec // failure injection, not security
	if rand.Uint64N(100) < s.Chaos.DatabaseFailureRate.Load() {
		err := fmt.Errorf("%w: database connection reset", ErrDependency)

		observability.RecordError(span, err)
		s.observability.Metrics.DependencyCalls.WithLabelValues("database", "error").Inc()

		return Order{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	order.ID = s.nextID
	order.Status = "pending"
	s.orders[order.ID] = order
	s.nextID++

	span.SetAttributes(attribute.Int64("order.id", order.ID))
	s.observability.Metrics.DependencyCalls.WithLabelValues("database", "success").Inc()

	return order, nil
}

// charge is the resilient call: breaker outermost, retries inside it, a
// per-attempt timeout innermost.
func (s *Service) charge(ctx context.Context, order Order) error {
	ctx, span := s.observability.Tracer.Start(ctx, "payments.Charge",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.Int64("payment.amount_cents", order.Cents)))
	defer span.End()

	start := time.Now()

	defer func() {
		s.observability.Metrics.DependencyDuration.
			WithLabelValues("payments").Observe(time.Since(start).Seconds())
	}()

	err := s.breaker.Do(ctx, func(ctx context.Context) error {
		return resilience.Do(ctx, s.retryPolicy, func(ctx context.Context) error {
			attemptCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()

			return s.callPaymentProvider(attemptCtx, order)
		})
	})

	switch {
	case err == nil:
		s.observability.Metrics.DependencyCalls.WithLabelValues("payments", "success").Inc()

	case errors.Is(err, resilience.ErrBreakerOpen):
		// Worth its own outcome label: "we did not even try" is a different
		// operational state from "we tried and it failed".
		s.observability.Metrics.DependencyCalls.WithLabelValues("payments", "breaker_open").Inc()
		span.SetAttributes(attribute.Bool("breaker.open", true))

	default:
		s.observability.Metrics.DependencyCalls.WithLabelValues("payments", "error").Inc()
	}

	return err
}

func (s *Service) callPaymentProvider(ctx context.Context, order Order) error {
	if err := s.simulateLatency(ctx); err != nil {
		return err
	}

	//nolint:gosec // failure injection
	if rand.Uint64N(100) < s.Chaos.PaymentFailureRate.Load() {
		return resilience.RetryableError{Err: fmt.Errorf("%w: payment provider 503", ErrDependency)}
	}

	return nil
}

func (s *Service) simulateLatency(ctx context.Context) error {
	//nolint:gosec // load shaping
	delay := time.Duration(2+rand.IntN(8)) * time.Millisecond

	//nolint:gosec // load shaping
	if rand.Uint64N(100) < s.Chaos.SlowRate.Load() {
		delay = time.Duration(200+rand.IntN(400)) * time.Millisecond
	}

	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) setStatus(id int64, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if order, found := s.orders[id]; found {
		order.Status = status
		s.orders[id] = order
	}
}

func (s *Service) ByID(ctx context.Context, id int64) (Order, error) {
	_, span := s.observability.Tracer.Start(ctx, "repository.ByID")
	defer span.End()

	s.mu.Lock()
	defer s.mu.Unlock()

	order, found := s.orders[id]
	if !found {
		return Order{}, fmt.Errorf("order %d: %w", id, ErrNotFound)
	}

	return order, nil
}

func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.orders)
}
