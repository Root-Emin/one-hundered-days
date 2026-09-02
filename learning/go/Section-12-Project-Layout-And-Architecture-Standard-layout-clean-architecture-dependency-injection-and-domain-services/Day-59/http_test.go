package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	service := NewOrderService(NewMemoryOrderRepository(), NewStaticCatalog(), SystemClock{})

	server := httptest.NewServer(NewHandler(service).Routes())

	t.Cleanup(server.Close)

	return server
}

func call(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	var buffer bytes.Buffer

	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, buffer.Bytes()
}

func TestOrderEndpointsHappyPath(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	status, body := call(t, server, http.MethodPost, "/orders", map[string]any{
		"customer_email": "ADA@example.com",
		"lines": []map[string]any{
			{"sku": "kb-01", "quantity": 2},
			{"sku": "ms-02", "quantity": 1},
		},
	})

	if status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", status, body)
	}

	var order orderResponse

	if err := json.Unmarshal(body, &order); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if order.Customer != "ada@example.com" || order.State != "draft" {
		t.Fatalf("order = %+v", order)
	}

	if order.Total != "307.00 EUR" {
		t.Fatalf("total = %q, want \"307.00 EUR\"", order.Total)
	}

	if status, body := call(t, server, http.MethodPost, "/orders/1/submit", nil); status != http.StatusOK {
		t.Fatalf("submit = %d (%s)", status, body)
	}

	if status, body := call(t, server, http.MethodPost, "/orders/1/pay", nil); status != http.StatusOK {
		t.Fatalf("pay = %d (%s)", status, body)
	}
}

// TestErrorMapping is the contract of the transport layer: each kind of domain
// error gets its own status code, and validation failures carry field detail.
func TestErrorMapping(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	if status, _ := call(t, server, http.MethodPost, "/orders", map[string]any{
		"customer_email": "ada@example.com",
		"lines":          []map[string]any{{"sku": "kb-01", "quantity": 1}},
	}); status != http.StatusCreated {
		t.Fatal("seed order failed")
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{
			"validation", http.MethodPost, "/orders",
			map[string]any{"customer_email": "nope", "lines": []map[string]any{{"sku": "", "quantity": 0}}},
			http.StatusUnprocessableEntity,
		},
		{
			"unknown product", http.MethodPost, "/orders",
			map[string]any{"customer_email": "ada@example.com", "lines": []map[string]any{{"sku": "zz-99", "quantity": 1}}},
			http.StatusNotFound,
		},
		{"missing order", http.MethodGet, "/orders/999", nil, http.StatusNotFound},
		{"invalid id", http.MethodGet, "/orders/abc", nil, http.StatusBadRequest},
		{
			"unknown field", http.MethodPost, "/orders",
			map[string]any{"customer_email": "ada@example.com", "discount": 90},
			http.StatusBadRequest,
		},
		{"bad state filter", http.MethodGet, "/orders?state=flying", nil, http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, server, test.method, test.path, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}
		})
	}

	// A state error is 409, distinct from a validation 422.
	if status, _ := call(t, server, http.MethodPost, "/orders/1/submit", nil); status != http.StatusOK {
		t.Fatal("submit failed")
	}

	status, body := call(t, server, http.MethodPost, "/orders/1/lines",
		map[string]any{"sku": "ms-02", "quantity": 1})

	if status != http.StatusConflict {
		t.Fatalf("adding to a submitted order = %d, want 409 (%s)", status, body)
	}
}

func TestValidationResponseCarriesFields(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	_, body := call(t, server, http.MethodPost, "/orders", map[string]any{
		"customer_email": "nope",
		"lines": []map[string]any{
			{"sku": "", "quantity": 0},
		},
	})

	var response errorResponse

	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(response.Fields) < 3 {
		t.Fatalf("fields = %+v, want one entry per broken rule", response.Fields)
	}

	seen := map[string]bool{}

	for _, field := range response.Fields {
		if field.Rule == "" || field.Message == "" {
			t.Fatalf("incomplete field detail: %+v", field)
		}

		seen[field.Field] = true
	}

	for _, expected := range []string{"email", "sku", "quantity"} {
		if !seen[expected] {
			t.Fatalf("no error reported for %q: %+v", expected, response.Fields)
		}
	}
}

// TestHandlersAreThin: the service is where the rules are, so calling it
// directly must produce exactly the same outcomes as calling the endpoint.
func TestHandlersAreThin(t *testing.T) {
	t.Parallel()

	service := NewOrderService(NewMemoryOrderRepository(), NewStaticCatalog(), SystemClock{})
	ctx := context.Background()

	order, err := service.CreateOrder(ctx, "ada@example.com", []LineRequest{{SKU: "kb-01", Quantity: 1}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := service.Submit(ctx, order.ID()); err != nil {
		t.Fatalf("submit: %v", err)
	}

	_, err = service.AddLine(ctx, order.ID(), LineRequest{SKU: "ms-02", Quantity: 1})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict - the rule must live below HTTP", err)
	}
}

func TestPriceComesFromTheCatalog(t *testing.T) {
	t.Parallel()

	service := NewOrderService(NewMemoryOrderRepository(), NewStaticCatalog(), SystemClock{})

	order, err := service.CreateOrder(context.Background(), "ada@example.com",
		[]LineRequest{{SKU: "kb-01", Quantity: 1}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The request never carried a price, and could not have.
	if order.Lines()[0].UnitPrice.Cents() != 12900 {
		t.Fatalf("unit price = %s, want the catalog price", order.Lines()[0].UnitPrice)
	}
}

func TestOrderCreatedAtUsesTheInjectedClock(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)

	service := NewOrderService(NewMemoryOrderRepository(), NewStaticCatalog(), stoppedClock{at: fixed})

	order, err := service.CreateOrder(context.Background(), "ada@example.com",
		[]LineRequest{{SKU: "kb-01", Quantity: 1}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !order.CreatedAt().Equal(fixed) {
		t.Fatalf("created at = %s, want %s", order.CreatedAt(), fixed)
	}
}

type stoppedClock struct {
	at time.Time
}

func (c stoppedClock) Now() time.Time { return c.at }
