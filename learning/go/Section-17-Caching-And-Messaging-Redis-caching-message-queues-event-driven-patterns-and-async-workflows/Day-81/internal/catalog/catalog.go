// Package catalog is the read-heavy service the cache sits in front of.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/cache"
)

var ErrNotFound = errors.New("product not found")

type Product struct {
	ID          int64     `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	PriceCents  int64     `json:"price_cents"`
	Stock       int       `json:"stock"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store stands in for the database. The artificial latency is what makes the
// cache's effect measurable.
type Store struct {
	mu       sync.RWMutex
	products map[int64]Product
	nextID   int64

	latency time.Duration
	queries atomic.Int64
}

func NewStore(latency time.Duration) *Store {
	return &Store{products: make(map[int64]Product), nextID: 1, latency: latency}
}

func (s *Store) Queries() int64 { return s.queries.Load() }

func (s *Store) simulateQuery(ctx context.Context) error {
	s.queries.Add(1)

	if s.latency <= 0 {
		return nil
	}

	select {
	case <-time.After(s.latency):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) Create(ctx context.Context, product Product) (Product, error) {
	if err := s.simulateQuery(ctx); err != nil {
		return Product{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	product.ID = s.nextID
	product.UpdatedAt = time.Now().UTC()
	s.products[product.ID] = product
	s.nextID++

	return product, nil
}

func (s *Store) ByID(ctx context.Context, id int64) (Product, error) {
	if err := s.simulateQuery(ctx); err != nil {
		return Product{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	product, found := s.products[id]
	if !found {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	return product, nil
}

func (s *Store) List(ctx context.Context) ([]Product, error) {
	if err := s.simulateQuery(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	products := make([]Product, 0, len(s.products))

	for _, product := range s.products {
		products = append(products, product)
	}

	return products, nil
}

func (s *Store) UpdatePrice(ctx context.Context, id int64, priceCents int64) (Product, error) {
	if err := s.simulateQuery(ctx); err != nil {
		return Product{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	product, found := s.products[id]
	if !found {
		return Product{}, fmt.Errorf("product %d: %w", id, ErrNotFound)
	}

	product.PriceCents = priceCents
	product.UpdatedAt = time.Now().UTC()
	s.products[id] = product

	return product, nil
}

//
// THE CACHED SERVICE
//

// Service implements cache-aside: read the cache, fall back to the store on a
// miss, and populate the cache with what it read.
type Service struct {
	store  *Store
	cache  cache.Cache
	logger *slog.Logger

	ttl time.Duration

	hits   atomic.Int64
	misses atomic.Int64

	// loader collapses concurrent misses for the same key into ONE load.
	//
	// Without it, a hot key expiring under load sends every waiting request
	// to the database at the same moment - the cache stampede. With it, one
	// request loads and the rest wait for its result.
	loader singleflight.Group
}

func NewService(store *Store, cacheImpl cache.Cache, ttl time.Duration, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	if ttl <= 0 {
		ttl = 30 * time.Second
	}

	return &Service{store: store, cache: cacheImpl, ttl: ttl, logger: logger}
}

func productKey(id int64) string { return "product:" + strconv.FormatInt(id, 10) }

const listKey = "product:list"

func (s *Service) Stats() (hits, misses int64) {
	return s.hits.Load(), s.misses.Load()
}

// ByID is the cache-aside read.
//
//  1. look in the cache
//  2. on a miss, read the source of truth
//  3. write what was read back into the cache, with a TTL
//
// The TTL is the safety net: even if an invalidation is missed, the entry
// cannot be wrong for longer than the TTL. That bound is what makes caching
// safe to reason about.
func (s *Service) ByID(ctx context.Context, id int64) (Product, error) {
	key := productKey(id)

	product, err := cache.GetJSON[Product](ctx, s.cache, key)

	switch {
	case err == nil:
		s.hits.Add(1)

		return product, nil

	case !errors.Is(err, cache.ErrMiss):
		// Any other error is still treated as a miss: a broken cache must not
		// break the read path.
		s.logger.Warn("cache read failed", slog.String("key", key), slog.String("error", err.Error()))
	}

	s.misses.Add(1)

	product, err = s.store.ByID(ctx, id)
	if err != nil {
		// Deliberately NOT caching the not-found result here. Negative
		// caching is a real technique (it stops a hot missing key hammering
		// the database), but it needs its own short TTL and its own
		// invalidation, so it is a decision rather than an accident.
		return Product{}, err
	}

	if err := cache.SetJSON(ctx, s.cache, key, product, s.ttl); err != nil {
		// A failed write is logged, not returned: the read succeeded.
		s.logger.Warn("cache write failed", slog.String("key", key), slog.String("error", err.Error()))
	}

	return product, nil
}

// ByIDSingleflight is ByID plus stampede protection.
//
// The difference only shows under concurrency: with 200 simultaneous readers
// of the same cold key, ByID issues ~200 database queries and this issues one.
func (s *Service) ByIDSingleflight(ctx context.Context, id int64) (Product, error) {
	key := productKey(id)

	product, err := cache.GetJSON[Product](ctx, s.cache, key)
	if err == nil {
		s.hits.Add(1)

		return product, nil
	}

	s.misses.Add(1)

	// Do blocks other callers with the same key until this one returns, then
	// hands them the same result. Only the first caller reaches the store.
	loaded, err, _ := s.loader.Do(key, func() (any, error) {
		// Re-check the cache: another goroutine may have populated it between
		// this one missing and reaching here.
		if cached, err := cache.GetJSON[Product](ctx, s.cache, key); err == nil {
			return cached, nil
		}

		fresh, err := s.store.ByID(ctx, id)
		if err != nil {
			return Product{}, err
		}

		if err := cache.SetJSON(ctx, s.cache, key, fresh, s.ttl); err != nil {
			s.logger.Warn("cache write failed", slog.String("key", key), slog.String("error", err.Error()))
		}

		return fresh, nil
	})
	if err != nil {
		return Product{}, err
	}

	return loaded.(Product), nil
}

func (s *Service) List(ctx context.Context) ([]Product, error) {
	products, err := cache.GetJSON[[]Product](ctx, s.cache, listKey)

	if err == nil {
		s.hits.Add(1)

		return products, nil
	}

	s.misses.Add(1)

	products, err = s.store.List(ctx)
	if err != nil {
		return nil, err
	}

	// A list is cached for a SHORTER time than a single item: it goes stale
	// whenever any member changes, so the window for being wrong is wider.
	if err := cache.SetJSON(ctx, s.cache, listKey, products, s.ttl/3); err != nil {
		s.logger.Warn("cache write failed", slog.String("key", listKey), slog.String("error", err.Error()))
	}

	return products, nil
}

func (s *Service) Create(ctx context.Context, product Product) (Product, error) {
	created, err := s.store.Create(ctx, product)
	if err != nil {
		return Product{}, err
	}

	// The new product changes the list, so the list entry must go.
	s.invalidate(ctx, listKey)

	return created, nil
}

// UpdatePrice shows the invalidation half of cache-aside.
//
// Order matters: write to the store FIRST, then invalidate. Doing it the other
// way round leaves a window where a concurrent read can repopulate the cache
// with the old value, which then survives for a whole TTL.
func (s *Service) UpdatePrice(ctx context.Context, id int64, priceCents int64) (Product, error) {
	product, err := s.store.UpdatePrice(ctx, id, priceCents)
	if err != nil {
		return Product{}, err
	}

	// Delete rather than overwrite. Writing the new value looks tempting, but
	// with two concurrent writers the later write can land first and the
	// cache ends up holding a value that never existed. Deleting means the
	// next reader loads the truth.
	s.invalidate(ctx, productKey(id), listKey)

	return product, nil
}

func (s *Service) invalidate(ctx context.Context, keys ...string) {
	if err := s.cache.Delete(ctx, keys...); err != nil {
		// A failed invalidation is serious: the cache now holds stale data
		// until the TTL expires. Log it loudly enough to alert on.
		s.logger.Error("cache invalidation failed",
			slog.Any("keys", keys), slog.String("error", err.Error()))
	}
}
