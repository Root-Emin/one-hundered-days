package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/catalog"
)

func newService(t *testing.T, ttl time.Duration) (*catalog.Service, *catalog.Store) {
	t.Helper()

	memory := cache.NewMemory(time.Minute)

	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Errorf("close cache: %v", err)
		}
	})

	// No artificial latency in tests: the assertions are about query COUNTS,
	// which are deterministic, not about timings, which are not.
	store := catalog.NewStore(0)

	return catalog.NewService(store, memory, ttl, nil), store
}

func seed(t *testing.T, service *catalog.Service, count int) {
	t.Helper()

	for i := range count {
		if _, err := service.Create(context.Background(), catalog.Product{
			SKU:        "SKU-1",
			Name:       "Product",
			PriceCents: int64(1000 * (i + 1)),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// TestCacheAside is the core behaviour: miss, load, populate, hit.
func TestCacheAside(t *testing.T) {
	t.Parallel()

	service, store := newService(t, time.Minute)

	seed(t, service, 1)

	queriesAfterSeed := store.Queries()

	if _, err := service.ByID(context.Background(), 1); err != nil {
		t.Fatalf("first read: %v", err)
	}

	if store.Queries() != queriesAfterSeed+1 {
		t.Fatalf("first read did not hit the store")
	}

	for range 10 {
		if _, err := service.ByID(context.Background(), 1); err != nil {
			t.Fatalf("cached read: %v", err)
		}
	}

	if store.Queries() != queriesAfterSeed+1 {
		t.Fatalf("cached reads hit the store %d extra times", store.Queries()-queriesAfterSeed-1)
	}

	hits, misses := service.Stats()

	if hits != 10 || misses != 1 {
		t.Fatalf("hits/misses = %d/%d, want 10/1", hits, misses)
	}
}

// TestInvalidationOnWrite: after a write, the next read must see the new value
// rather than the cached one.
func TestInvalidationOnWrite(t *testing.T) {
	t.Parallel()

	service, _ := newService(t, time.Hour) // a long TTL, so only invalidation can help

	seed(t, service, 1)

	ctx := context.Background()

	if _, err := service.ByID(ctx, 1); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	if _, err := service.UpdatePrice(ctx, 1, 9999); err != nil {
		t.Fatalf("update: %v", err)
	}

	product, err := service.ByID(ctx, 1)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}

	if product.PriceCents != 9999 {
		t.Fatalf("price = %d, want 9999 - the cache was not invalidated", product.PriceCents)
	}
}

// TestListInvalidatedByCreate: a new product changes the list, so the list
// entry must be dropped too. Forgetting this is the most common cache bug.
func TestListInvalidatedByCreate(t *testing.T) {
	t.Parallel()

	service, _ := newService(t, time.Hour)

	seed(t, service, 2)

	ctx := context.Background()

	before, err := service.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(before) != 2 {
		t.Fatalf("list = %d, want 2", len(before))
	}

	seed(t, service, 1)

	after, err := service.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(after) != 3 {
		t.Fatalf("list after create = %d, want 3 - the list cache went stale", len(after))
	}
}

func TestTTLBoundsStaleness(t *testing.T) {
	t.Parallel()

	// A very short TTL, and an update that deliberately bypasses the service
	// (as a background job or another instance would).
	service, store := newService(t, 20*time.Millisecond)

	seed(t, service, 1)

	ctx := context.Background()

	if _, err := service.ByID(ctx, 1); err != nil {
		t.Fatalf("warm: %v", err)
	}

	if _, err := store.UpdatePrice(ctx, 1, 5555); err != nil {
		t.Fatalf("out-of-band update: %v", err)
	}

	// Immediately: still the cached value. That is the staleness window, and
	// it is a deliberate trade, not a bug.
	product, err := service.ByID(ctx, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if product.PriceCents == 5555 {
		t.Fatal("the cache returned a value it could not have known yet")
	}

	time.Sleep(30 * time.Millisecond)

	// After the TTL: fresh, without anybody invalidating anything.
	if product, err = service.ByID(ctx, 1); err != nil {
		t.Fatalf("read after ttl: %v", err)
	}

	if product.PriceCents != 5555 {
		t.Fatalf("price = %d after the TTL, want 5555", product.PriceCents)
	}
}

// TestStampede compares the naive read with the singleflight one under the
// exact condition that matters: many readers, one cold key.
func TestStampede(t *testing.T) {
	t.Parallel()

	service, store := newService(t, time.Minute)

	seed(t, service, 1)

	ctx := context.Background()

	const readers = 50

	// Naive: every concurrent reader misses and loads.
	before := store.Queries()

	var waitGroup sync.WaitGroup

	for range readers {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			if _, err := service.ByID(ctx, 1); err != nil {
				t.Errorf("read: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	naive := store.Queries() - before

	// Singleflight, same cold start.
	if _, err := service.UpdatePrice(ctx, 1, 1234); err != nil { // clears the key
		t.Fatalf("invalidate: %v", err)
	}

	before = store.Queries()

	waitGroup = sync.WaitGroup{}

	for range readers {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			if _, err := service.ByIDSingleflight(ctx, 1); err != nil {
				t.Errorf("read: %v", err)
			}
		}()
	}

	waitGroup.Wait()

	collapsed := store.Queries() - before

	t.Logf("%d concurrent readers: %d queries naive, %d with singleflight", readers, naive, collapsed)

	if collapsed > 2 {
		t.Fatalf("singleflight issued %d queries, want at most 2", collapsed)
	}

	if collapsed >= naive {
		t.Fatalf("singleflight (%d) did not reduce the load (%d)", collapsed, naive)
	}
}

func TestMissingProductIsNotCached(t *testing.T) {
	t.Parallel()

	service, store := newService(t, time.Minute)

	ctx := context.Background()

	before := store.Queries()

	for range 3 {
		if _, err := service.ByID(ctx, 999); !errors.Is(err, catalog.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}

	// Every lookup reached the store: negative results are deliberately not
	// cached here, so a product created a moment later is visible at once.
	if store.Queries()-before != 3 {
		t.Fatalf("queries = %d, want one per lookup", store.Queries()-before)
	}
}
