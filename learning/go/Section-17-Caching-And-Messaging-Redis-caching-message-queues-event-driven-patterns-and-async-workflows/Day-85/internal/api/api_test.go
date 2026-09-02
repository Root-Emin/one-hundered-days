package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/api"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store, *cache.Memory) {
	t.Helper()

	db, err := store.Open("file:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	dataStore := store.New(db)
	productCache := cache.NewMemory()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(api.New(dataStore, productCache, logger).Routes())

	t.Cleanup(server.Close)

	return server, dataStore, productCache
}

func do(t *testing.T, server *httptest.Server, method, path string, body any, target any) *http.Response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	})

	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}

	return response
}

func createProduct(t *testing.T, server *httptest.Server, price int64) store.Product {
	t.Helper()

	var product store.Product

	response := do(t, server, http.MethodPost, "/products",
		map[string]any{"name": "keyboard", "price_cent": price}, &product)

	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create product status = %d, want 201", response.StatusCode)
	}

	return product
}

func TestReadIsServedFromCacheOnTheSecondCall(t *testing.T) {
	server, _, _ := newServer(t)

	product := createProduct(t, server, 12000)
	path := fmt.Sprintf("/products/%d", product.ID)

	var first store.Product

	response := do(t, server, http.MethodGet, path, nil, &first)

	if got := response.Header.Get("X-Cache"); got != "MISS" {
		t.Errorf("first read X-Cache = %q, want MISS", got)
	}

	var second store.Product

	response = do(t, server, http.MethodGet, path, nil, &second)

	if got := response.Header.Get("X-Cache"); got != "HIT" {
		t.Errorf("second read X-Cache = %q, want HIT", got)
	}

	if second.PriceCent != first.PriceCent {
		t.Errorf("cached price = %d, want %d", second.PriceCent, first.PriceCent)
	}
}

// The invalidation is the part that is easy to forget and impossible to
// notice: without it the API keeps serving the old price for a full TTL.
func TestUpdateInvalidatesTheCachedRead(t *testing.T) {
	server, _, productCache := newServer(t)

	product := createProduct(t, server, 12000)
	path := fmt.Sprintf("/products/%d", product.ID)

	do(t, server, http.MethodGet, path, nil, nil)

	if productCache.Len() != 1 {
		t.Fatalf("cache holds %d entries after a read, want 1", productCache.Len())
	}

	do(t, server, http.MethodPut, path, map[string]any{"price_cent": 9900}, nil)

	if productCache.Len() != 0 {
		t.Fatal("the cache entry survived the update; readers would serve the old price")
	}

	var refreshed store.Product

	response := do(t, server, http.MethodGet, path, nil, &refreshed)

	if got := response.Header.Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache after update = %q, want MISS", got)
	}

	if refreshed.PriceCent != 9900 {
		t.Errorf("price after update = %d, want 9900", refreshed.PriceCent)
	}

	if refreshed.Version != product.Version+1 {
		t.Errorf("version = %d, want %d", refreshed.Version, product.Version+1)
	}
}

// TTL is the backstop for an invalidation that never happened.
func TestCachedEntryExpires(t *testing.T) {
	server, dataStore, productCache := newServer(t)

	now := time.Now()
	productCache.SetClock(func() time.Time { return now })

	product := createProduct(t, server, 12000)
	path := fmt.Sprintf("/products/%d", product.ID)

	do(t, server, http.MethodGet, path, nil, nil)

	// Update the row behind the API's back, so nothing invalidates the entry.
	if _, err := dataStore.UpdatePrice(t.Context(), product.ID, 100); err != nil {
		t.Fatalf("update price: %v", err)
	}

	var stale store.Product

	do(t, server, http.MethodGet, path, nil, &stale)

	if stale.PriceCent != 12000 {
		t.Fatalf("price = %d, want the stale 12000 (that is the premise of the test)", stale.PriceCent)
	}

	now = now.Add(api.ProductTTL + time.Second)

	var fresh store.Product

	response := do(t, server, http.MethodGet, path, nil, &fresh)

	if got := response.Header.Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache after the TTL = %q, want MISS", got)
	}

	if fresh.PriceCent != 100 {
		t.Errorf("price after the TTL = %d, want 100", fresh.PriceCent)
	}
}

// A cache outage must degrade to a slower read, never to an error.
func TestBrokenCacheStillServesReads(t *testing.T) {
	db, err := store.Open("file:" + filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	dataStore := store.New(db)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(api.New(dataStore, brokenCache{}, logger).Routes())

	t.Cleanup(server.Close)

	product := createProduct(t, server, 12000)

	var fetched store.Product

	response := do(t, server, http.MethodGet, fmt.Sprintf("/products/%d", product.ID), nil, &fetched)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with the cache down", response.StatusCode)
	}

	if fetched.PriceCent != 12000 {
		t.Errorf("price = %d, want 12000", fetched.PriceCent)
	}
}

type brokenCache struct{}

func (brokenCache) Get(context.Context, string) ([]byte, error) {
	return nil, errCacheDown
}

func (brokenCache) Set(context.Context, string, []byte, time.Duration) error {
	return errCacheDown
}

func (brokenCache) Delete(context.Context, ...string) error {
	return errCacheDown
}

// PlaceOrder returns 202: the order is durable, the receipt is not written yet.
func TestPlaceOrderIsAcceptedAndWritesAnEvent(t *testing.T) {
	server, dataStore, _ := newServer(t)

	product := createProduct(t, server, 12000)

	var placed store.Order

	response := do(t, server, http.MethodPost, "/orders",
		map[string]any{"product_id": product.ID, "quantity": 2}, &placed)

	if response.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202 (the work is not finished yet)", response.StatusCode)
	}

	if placed.Status != "placed" {
		t.Errorf("status = %q, want placed", placed.Status)
	}

	if placed.AmountCent != 24000 {
		t.Errorf("amount = %d, want 24000", placed.AmountCent)
	}

	pending, err := dataStore.PendingEventCount(t.Context())
	if err != nil {
		t.Fatalf("pending events: %v", err)
	}

	if pending != 1 {
		t.Errorf("pending events = %d, want 1", pending)
	}
}

func TestMissingProductIs404(t *testing.T) {
	server, _, _ := newServer(t)

	response := do(t, server, http.MethodGet, "/products/999", nil, nil)

	if response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.StatusCode)
	}
}

func TestInvalidOrderIsRejected(t *testing.T) {
	server, _, _ := newServer(t)

	response := do(t, server, http.MethodPost, "/orders",
		map[string]any{"product_id": 1, "quantity": 0}, nil)

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}

var errCacheDown = errors.New("cache is down")
