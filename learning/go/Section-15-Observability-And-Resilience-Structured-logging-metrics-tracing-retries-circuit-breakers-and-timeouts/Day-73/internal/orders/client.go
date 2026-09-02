package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-73/internal/tracing"
)

// PaymentsClient makes the outbound call that carries the trace across a
// process boundary.
type PaymentsClient struct {
	baseURL string
	client  *http.Client
	tracer  trace.Tracer
}

func NewPaymentsClient(baseURL string, client *http.Client) *PaymentsClient {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}

	return &PaymentsClient{
		baseURL: baseURL,
		client:  client,
		tracer:  tracing.Tracer("orders/payments-client"),
	}
}

func (c *PaymentsClient) Charge(ctx context.Context, orderID, cents int64) error {
	ctx, span := c.tracer.Start(ctx, "payments.Charge",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.Int64("order.id", orderID),
			attribute.Int64("payment.amount_cents", cents),
			attribute.String("server.address", c.baseURL),
		))
	defer span.End()

	payload, err := json.Marshal(map[string]int64{"order_id": orderID, "cents": cents})
	if err != nil {
		tracing.RecordError(span, err)

		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/charges", bytes.NewReader(payload))
	if err != nil {
		tracing.RecordError(span, err)

		return err
	}

	request.Header.Set("Content-Type", "application/json")

	// THE line that makes distributed tracing work: the current span context
	// is written into the outgoing headers as W3C traceparent. Remove it and
	// the payments service starts an unrelated trace.
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))

	response, err := c.client.Do(request)
	if err != nil {
		tracing.RecordError(span, err)

		return fmt.Errorf("charge order %d: %w", orderID, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			slog.Warn("close payments response", slog.String("error", err.Error()))
		}
	}()

	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))

	if response.StatusCode >= 400 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
		if readErr != nil {
			body = []byte("(unreadable body)")
		}

		err := fmt.Errorf("payments returned %d: %s", response.StatusCode, bytes.TrimSpace(body))

		tracing.RecordError(span, err)

		return err
	}

	return nil
}
