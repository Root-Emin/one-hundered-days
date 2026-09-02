// Package api is the synchronous half of the MVP.
//
// Two flows worth reading closely:
//
//	GET /products/{id}  cache-aside: look in the cache, fall back to the
//	                    database, populate on the way out.
//	PUT /products/{id}  write to the database, THEN invalidate. The ordering
//	                    is the whole game - see the comment on updateProduct.
//
// POST /orders returns as soon as the order and its event are committed. The
// receipt is written by the worker afterwards, so the response says
// "status: placed", not "confirmed". Telling the client the truth about what
// has actually happened yet is part of designing an async system.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-85/internal/store"
)

// ProductTTL bounds staleness if an invalidation is ever missed - a crash
// between the commit and the delete, a cache node that was unreachable. It is
// the backstop, not the strategy.
const ProductTTL = 30 * time.Second

type API struct {
	store  *store.Store
	cache  cache.Cache
	logger *slog.Logger

	cacheHits   atomic.Int64
	cacheMisses atomic.Int64
}

func New(s *store.Store, c cache.Cache, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}

	return &API{store: s, cache: c, logger: logger}
}

func (a *API) CacheStats() (hits, misses int64) {
	return a.cacheHits.Load(), a.cacheMisses.Load()
}

func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /products/{id}", a.getProduct)
	mux.HandleFunc("POST /products", a.createProduct)
	mux.HandleFunc("PUT /products/{id}", a.updateProduct)
	mux.HandleFunc("POST /orders", a.placeOrder)
	mux.HandleFunc("GET /orders/{id}", a.getOrder)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

// getProduct is cache-aside.
//
// A cache failure must never fail the request: cache.ErrMiss and a Redis
// timeout take the same path, straight to the database. A cache that can take
// the site down is worse than no cache.
func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	key := cache.ProductKey(id)

	product, err := cache.GetJSON[store.Product](r.Context(), a.cache, key)
	if err == nil {
		a.cacheHits.Add(1)

		w.Header().Set("X-Cache", "HIT")
		writeJSON(w, http.StatusOK, product)

		return
	}

	if !errors.Is(err, cache.ErrMiss) {
		// Degraded, not broken: log it and read through.
		a.logger.Warn("cache read failed", slog.String("key", key), slog.String("error", err.Error()))
	}

	a.cacheMisses.Add(1)

	product, err = a.store.Product(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	if err := cache.SetJSON(r.Context(), a.cache, key, product, ProductTTL); err != nil {
		a.logger.Warn("cache write failed", slog.String("key", key), slog.String("error", err.Error()))
	}

	w.Header().Set("X-Cache", "MISS")
	writeJSON(w, http.StatusOK, product)
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		PriceCent int64  `json:"price_cent"`
	}

	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if body.Name == "" || body.PriceCent <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("name and a positive price_cent are required"))

		return
	}

	product, err := a.store.CreateProduct(r.Context(), body.Name, body.PriceCent)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, product)
}

// updateProduct writes THEN invalidates.
//
// The race window this ordering closes: if the cache entry is deleted first,
// a concurrent reader can miss, read the OLD row from the database, and write
// it back to the cache after the update commits. The stale value then lives
// for a full TTL. Committing first means any reader that repopulates the cache
// after our delete reads the new row.
//
// The remaining window - a crash between the commit and the delete - is what
// the TTL is for.
func (a *API) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var body struct {
		PriceCent int64 `json:"price_cent"`
	}

	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if body.PriceCent <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("price_cent must be positive"))

		return
	}

	product, err := a.store.UpdatePrice(r.Context(), id, body.PriceCent)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	// Delete, do not overwrite. Writing the new value here would race with a
	// concurrent update and could leave the older of the two in the cache.
	if err := a.cache.Delete(r.Context(), cache.ProductKey(id)); err != nil {
		a.logger.Error("invalidation failed",
			slog.String("key", cache.ProductKey(id)), slog.String("error", err.Error()))
	}

	writeJSON(w, http.StatusOK, product)
}

// placeOrder commits the order and its event, then returns. The receipt
// happens later, in the worker.
func (a *API) placeOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProductID int64 `json:"product_id"`
		Quantity  int64 `json:"quantity"`
	}

	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if body.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("quantity must be positive"))

		return
	}

	order, err := a.store.PlaceOrder(r.Context(), body.ProductID, body.Quantity)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	// 202, not 201: the order exists, but the work it triggers has not
	// finished. The status field tells the client where to look next.
	writeJSON(w, http.StatusAccepted, order)
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	order, err := a.store.Order(r.Context(), id)
	if err != nil {
		a.writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, order)
}

func (a *API) writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err)

		return
	}

	a.logger.Error("request failed", slog.String("error", err.Error()))

	writeError(w, http.StatusInternalServerError, errors.New("internal error"))
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q", r.PathValue("id"))
	}

	return id, nil
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already sent; there is nothing left to say.
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
