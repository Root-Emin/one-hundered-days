package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/catalog"
	"example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-92/internal/httpapi"
)

func newServer(t *testing.T) (*httptest.Server, *catalog.Store) {
	t.Helper()

	store := catalog.New()

	server := httptest.NewServer(httpapi.New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())

	t.Cleanup(server.Close)

	return server, store
}

func request(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	})

	return response
}

func decodeInto(t *testing.T, response *http.Response, target any) {
	t.Helper()

	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestAddAndFetchAProduct(t *testing.T) {
	server, _ := newServer(t)

	response := request(t, server, http.MethodPost, "/products", map[string]any{
		"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 5,
	})

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", response.StatusCode)
	}

	var created catalog.Product

	decodeInto(t, response, &created)

	if created.SKU != "KB-01" || created.UpdatedAt.IsZero() {
		t.Errorf("created = %+v", created)
	}

	fetched := request(t, server, http.MethodGet, "/products/KB-01", nil)

	if fetched.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", fetched.StatusCode)
	}
}

// Every non-2xx uses one body shape, so a client writes one decoder instead of
// five - and gets one of them wrong.
func TestErrorsShareOneShape(t *testing.T) {
	server, _ := newServer(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"unknown sku", http.MethodGet, "/products/nope", nil, http.StatusNotFound, "not_found"},
		{
			"invalid product", http.MethodPost, "/products",
			map[string]any{"sku": "", "name": "x", "price_cent": 1, "stock": 0},
			http.StatusBadRequest, "invalid_request",
		},
		{
			"reserve missing product", http.MethodPost, "/products/nope/reservations",
			map[string]any{"quantity": 1}, http.StatusNotFound, "not_found",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := request(t, server, testCase.method, testCase.path, testCase.body)

			if response.StatusCode != testCase.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, testCase.status)
			}

			var body httpapi.ErrorResponse

			decodeInto(t, response, &body)

			if body.Error != testCase.code {
				t.Errorf("error code = %q, want %q", body.Error, testCase.code)
			}

			if body.Message == "" {
				t.Error("error body has no message")
			}
		})
	}
}

func TestDuplicateSKUIsAConflict(t *testing.T) {
	server, _ := newServer(t)

	product := map[string]any{"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 5}

	request(t, server, http.MethodPost, "/products", product)

	response := request(t, server, http.MethodPost, "/products", product)

	if response.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", response.StatusCode)
	}
}

// Insufficient stock is 409, not 400: the request was well formed, the state
// said no. A 400 would tell the client to fix its request, which will not help.
func TestInsufficientStockIsAConflictAndChangesNothing(t *testing.T) {
	server, store := newServer(t)

	request(t, server, http.MethodPost, "/products",
		map[string]any{"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 2})

	response := request(t, server, http.MethodPost, "/products/KB-01/reservations",
		map[string]any{"quantity": 5})

	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.StatusCode)
	}

	product, err := store.Get("KB-01")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if product.Stock != 2 {
		t.Errorf("stock = %d, want 2 - a failed reservation must change nothing", product.Stock)
	}
}

func TestReserveDecrementsStock(t *testing.T) {
	server, _ := newServer(t)

	request(t, server, http.MethodPost, "/products",
		map[string]any{"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 5})

	response := request(t, server, http.MethodPost, "/products/KB-01/reservations",
		map[string]any{"quantity": 2})

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	var product catalog.Product

	decodeInto(t, response, &product)

	if product.Stock != 3 {
		t.Errorf("stock = %d, want 3", product.Stock)
	}
}

func TestDeleteReturns204ThenNotFound(t *testing.T) {
	server, _ := newServer(t)

	request(t, server, http.MethodPost, "/products",
		map[string]any{"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 5})

	response := request(t, server, http.MethodDelete, "/products/KB-01", nil)

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.StatusCode)
	}

	again := request(t, server, http.MethodDelete, "/products/KB-01", nil)

	if again.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", again.StatusCode)
	}
}

// The list order is part of the contract: an endpoint returning the same data
// in a different order every call breaks caching and every test that uses it.
func TestListIsOrderedBySKU(t *testing.T) {
	server, _ := newServer(t)

	for _, sku := range []string{"MS-02", "KB-01", "SC-03"} {
		request(t, server, http.MethodPost, "/products",
			map[string]any{"sku": sku, "name": sku, "price_cent": 100, "stock": 1})
	}

	response := request(t, server, http.MethodGet, "/products", nil)

	var products []catalog.Product

	decodeInto(t, response, &products)

	if len(products) != 3 {
		t.Fatalf("products = %d, want 3", len(products))
	}

	for i := 1; i < len(products); i++ {
		if products[i-1].SKU > products[i].SKU {
			t.Fatalf("out of order: %s before %s", products[i-1].SKU, products[i].SKU)
		}
	}
}

// Every status in the route table must be reachable, or the table (and the
// spec derived from it) is fiction.
func TestRouteTableIsComplete(t *testing.T) {
	api := httpapi.New(catalog.New(), nil)

	routes := api.Routes()

	if len(routes) == 0 {
		t.Fatal("no routes")
	}

	for _, route := range routes {
		if route.Summary == "" {
			t.Errorf("%s %s has no summary", route.Method, route.Pattern)
		}

		if len(route.Statuses) == 0 {
			t.Errorf("%s %s declares no status codes", route.Method, route.Pattern)
		}
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	server, _ := newServer(t)

	response := request(t, server, http.MethodPost, "/products", map[string]any{
		"sku": "KB-01", "name": "Keyboard", "price_cent": 12000, "stock": 5, "colour": "black",
	})

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 - a typo'd field should not be silently dropped", response.StatusCode)
	}
}
