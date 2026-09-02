package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

/*
HTTP-level integration tests: the full stack, from JSON in to SQL and back.

These cover the contract the API promises - status codes, DTO shape, and the
mapping from domain errors to HTTP - which unit tests of the service cannot.
*/

func newTestAPI(t *testing.T) *httptest.Server {
	t.Helper()

	db := newTestDB(t)
	shop := NewShopService(RepositoriesFor(db), NewSQLTxManager(db))

	server := httptest.NewServer(NewAPI(shop, db).Routes())

	t.Cleanup(server.Close)

	return server
}

func do(t *testing.T, server *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()

	payload := bytes.NewReader(nil)

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

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

func decodeInto[T any](t *testing.T, body []byte) T {
	t.Helper()

	var value T

	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return value
}

func TestAPIHappyPath(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t)

	status, body := do(t, server, http.MethodGet, "/healthz", nil)
	if status != http.StatusOK {
		t.Fatalf("healthz = %d (%s)", status, body)
	}

	status, body = do(t, server, http.MethodPost, "/customers",
		map[string]string{"email": "api@example.com", "name": "API Tester"})

	if status != http.StatusCreated {
		t.Fatalf("create customer = %d (%s)", status, body)
	}

	customer := decodeInto[customerResponse](t, body)

	status, body = do(t, server, http.MethodPost, "/products",
		map[string]any{"sku": "api-1", "name": "Thing", "price_cents": 2500, "stock": 4})

	if status != http.StatusCreated {
		t.Fatalf("create product = %d (%s)", status, body)
	}

	product := decodeInto[productResponse](t, body)

	if product.Price != "25.00" {
		t.Fatalf("price = %q, want \"25.00\"", product.Price)
	}

	status, body = do(t, server, http.MethodPost, "/orders", map[string]any{
		"customer_id": mustAtoi(t, customer.ID),
		"lines":       []map[string]any{{"product_id": mustAtoi(t, product.ID), "quantity": 2}},
	})

	if status != http.StatusCreated {
		t.Fatalf("place order = %d (%s)", status, body)
	}

	order := decodeInto[orderResponse](t, body)

	if order.Total != "50.00" || order.Status != "placed" || len(order.Items) != 1 {
		t.Fatalf("order = %+v", order)
	}

	status, body = do(t, server, http.MethodGet, "/customers/"+customer.ID+"/orders", nil)
	if status != http.StatusOK {
		t.Fatalf("list orders = %d (%s)", status, body)
	}

	list := decodeInto[struct {
		Count int `json:"count"`
	}](t, body)

	if list.Count != 1 {
		t.Fatalf("order count = %d, want 1", list.Count)
	}

	status, body = do(t, server, http.MethodPost, "/orders/"+order.ID+"/cancel", nil)
	if status != http.StatusOK {
		t.Fatalf("cancel = %d (%s)", status, body)
	}

	if cancelled := decodeInto[orderResponse](t, body); cancelled.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}

	// Stock came back with the cancellation.
	status, body = do(t, server, http.MethodGet, "/products", nil)
	if status != http.StatusOK {
		t.Fatalf("list products = %d (%s)", status, body)
	}

	products := decodeInto[struct {
		Products []productResponse `json:"products"`
	}](t, body)

	if products.Products[0].Stock != 4 {
		t.Fatalf("stock = %d, want 4 after cancellation", products.Products[0].Stock)
	}
}

func TestAPIErrorMapping(t *testing.T) {
	t.Parallel()

	server := newTestAPI(t)

	// Seed one customer and a scarce product.
	_, body := do(t, server, http.MethodPost, "/customers",
		map[string]string{"email": "errors@example.com", "name": "Tester"})
	customer := decodeInto[customerResponse](t, body)

	_, body = do(t, server, http.MethodPost, "/products",
		map[string]any{"sku": "scarce", "name": "Scarce", "price_cents": 100, "stock": 1})
	product := decodeInto[productResponse](t, body)

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{
			"duplicate customer", http.MethodPost, "/customers",
			map[string]string{"email": "errors@example.com", "name": "Copy"},
			http.StatusConflict,
		},
		{
			"invalid email", http.MethodPost, "/customers",
			map[string]string{"email": "nope", "name": "X"},
			http.StatusUnprocessableEntity,
		},
		{
			"unknown field", http.MethodPost, "/customers",
			map[string]string{"e-mail": "a@b.c"},
			http.StatusBadRequest,
		},
		{
			"missing customer", http.MethodGet, "/customers/9999", nil,
			http.StatusNotFound,
		},
		{
			"invalid id", http.MethodGet, "/customers/abc", nil,
			http.StatusBadRequest,
		},
		{
			"order for missing customer", http.MethodPost, "/orders",
			map[string]any{
				"customer_id": 9999,
				"lines":       []map[string]any{{"product_id": mustAtoi(t, product.ID), "quantity": 1}},
			},
			http.StatusNotFound,
		},
		{
			"oversell", http.MethodPost, "/orders",
			map[string]any{
				"customer_id": mustAtoi(t, customer.ID),
				"lines":       []map[string]any{{"product_id": mustAtoi(t, product.ID), "quantity": 99}},
			},
			http.StatusConflict,
		},
		{
			"empty order", http.MethodPost, "/orders",
			map[string]any{"customer_id": mustAtoi(t, customer.ID), "lines": []map[string]any{}},
			http.StatusUnprocessableEntity,
		},
		{
			"missing order", http.MethodGet, "/orders/9999", nil,
			http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := do(t, server, test.method, test.path, test.body)

			if status != test.want {
				t.Fatalf("status = %d, want %d (%s)", status, test.want, body)
			}

			// No internal detail may reach the client.
			if bytes.Contains(bytes.ToLower(body), []byte("sql")) {
				t.Fatalf("response leaks database detail: %s", body)
			}
		})
	}
}

func mustAtoi(t *testing.T, value string) int64 {
	t.Helper()

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse id %q: %v", value, err)
	}

	return parsed
}
