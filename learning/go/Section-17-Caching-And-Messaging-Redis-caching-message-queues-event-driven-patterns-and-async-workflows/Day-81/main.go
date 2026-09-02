package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/cache"
	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/catalog"
)

/*
Day 81 - Caching & Messaging: In-Memory and Redis Caching

Tasks covered:

 1. Cache candidates chosen deliberately: read-heavy, tolerant of brief
    staleness (see the table this program prints)
 2. A Redis client with timeouts and a key prefix (internal/cache/redis.go)
 3. Cache-aside: read cache, miss, load, populate (internal/catalog)
 4. Invalidation on write, in the right order, with a TTL as the backstop

Run:

	go run .                       # in-memory cache
	REDIS_ADDR=localhost:6379 go run .

	# a Redis to try it against:
	docker run --rm -p 6379:6379 redis:7-alpine

Test:

	go test ./...     # runs against miniredis, so no Redis is needed
*/

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn, // the demo prints its own output
	}))

	if err := run(logger); err != nil {
		fmt.Fprintf(os.Stderr, "day81: %v\n", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	backend, closeCache, err := openCache(ctx)
	if err != nil {
		return err
	}

	defer closeCache()

	// 25ms per query: what a real database call costs across a network.
	store := catalog.NewStore(25 * time.Millisecond)
	service := catalog.NewService(store, backend, 30*time.Second, logger)

	for i := range 5 {
		if _, err := service.Create(ctx, catalog.Product{
			SKU:        fmt.Sprintf("SKU-%03d", i+1),
			Name:       fmt.Sprintf("Product %d", i+1),
			PriceCents: int64(1000 * (i + 1)),
			Stock:      10,
		}); err != nil {
			return err
		}
	}

	fmt.Println("\n1) Cold read vs warm read")
	fmt.Println(strings.Repeat("-", 78))

	start := time.Now()

	if _, err := service.ByID(ctx, 1); err != nil {
		return err
	}

	cold := time.Since(start)

	start = time.Now()

	if _, err := service.ByID(ctx, 1); err != nil {
		return err
	}

	warm := time.Since(start)

	fmt.Printf("  first read  (miss -> database): %s\n", cold.Round(time.Microsecond))
	fmt.Printf("  second read (hit  -> cache)   : %s\n", warm.Round(time.Microsecond))
	fmt.Printf("  %.0fx faster\n", float64(cold)/float64(warm))

	fmt.Println("\n2) Under load")
	fmt.Println(strings.Repeat("-", 78))

	before := store.Queries()

	var waitGroup sync.WaitGroup

	start = time.Now()

	// 200 reads spread over 5 products.
	for i := range 200 {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			if _, err := service.ByID(ctx, int64(i%5)+1); err != nil {
				logger.Error("read", slog.String("error", err.Error()))
			}
		}(i)
	}

	waitGroup.Wait()

	elapsed := time.Since(start)
	queries := store.Queries() - before
	hits, misses := service.Stats()

	fmt.Printf("  200 reads in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  database queries: %d (without a cache it would be 200)\n", queries)
	fmt.Printf("  hit ratio: %.1f%% (%d hits, %d misses)\n",
		float64(hits)/float64(hits+misses)*100, hits, misses)

	fmt.Println("\n  Note how many queries that still is. 200 readers arrived while the")
	fmt.Println("  keys were cold, all missed, and all went to the database together -")
	fmt.Println("  the cache STAMPEDE. The same load with singleflight:")

	// Same load, cold cache, but concurrent misses for one key are collapsed
	// into a single load.
	if err := backend.Delete(ctx, "product:1", "product:2", "product:3", "product:4", "product:5"); err != nil {
		return err
	}

	before = store.Queries()

	waitGroup = sync.WaitGroup{}

	start = time.Now()

	for i := range 200 {
		waitGroup.Add(1)

		go func(i int) {
			defer waitGroup.Done()

			if _, err := service.ByIDSingleflight(ctx, int64(i%5)+1); err != nil {
				logger.Error("read", slog.String("error", err.Error()))
			}
		}(i)
	}

	waitGroup.Wait()

	fmt.Printf("\n  200 reads in %s, database queries: %d (one per distinct key)\n",
		time.Since(start).Round(time.Millisecond), store.Queries()-before)

	fmt.Println("\n3) Invalidation on write")
	fmt.Println(strings.Repeat("-", 78))

	product, err := service.ByID(ctx, 1)
	if err != nil {
		return err
	}

	fmt.Printf("  cached price: %d cents\n", product.PriceCents)

	if _, err := service.UpdatePrice(ctx, 1, 4242); err != nil {
		return err
	}

	fmt.Println("  price updated to 4242 cents (write, then invalidate)")

	product, err = service.ByID(ctx, 1)
	if err != nil {
		return err
	}

	fmt.Printf("  next read   : %d cents", product.PriceCents)

	if product.PriceCents == 4242 {
		fmt.Println("  <- fresh, because the key was deleted")
	} else {
		fmt.Println("  <- STALE: the invalidation did not happen")
	}

	printCandidates()
	printPitfalls()

	return nil
}

func openCache(ctx context.Context) (cache.Cache, func(), error) {
	address := strings.TrimSpace(os.Getenv("REDIS_ADDR"))

	if address == "" {
		fmt.Println("\nUsing the in-process cache (set REDIS_ADDR to use Redis)")

		memory := cache.NewMemory(time.Minute)

		return memory, func() {
			if err := memory.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "close cache: %v\n", err)
			}
		}, nil
	}

	client := cache.NewRedis(cache.DefaultRedisConfig(address))

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx); err != nil {
		return nil, nil, fmt.Errorf("redis at %s: %w", address, err)
	}

	fmt.Printf("\nUsing Redis at %s\n", address)

	return client, func() {
		if err := client.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close redis: %v\n", err)
		}
	}, nil
}

func printCandidates() {
	fmt.Println("\n4) What to cache, and what not to")
	fmt.Println(strings.Repeat("-", 78))

	rows := []struct {
		data  string
		cache string
		ttl   string
		why   string
	}{
		{"Product catalog", "yes", "5-30 min", "read constantly, changes rarely, staleness is harmless"},
		{"Public config / feature flags", "yes", "1-5 min", "same, but a shorter window matters for rollouts"},
		{"Reference data (countries)", "yes", "hours", "effectively immutable"},
		{"A rendered list page", "yes", "seconds", "goes stale when ANY member changes"},
		{"User's own profile", "careful", "seconds", "the user notices staleness in their own data immediately"},
		{"Shopping cart", "NO", "-", "write-heavy and personal; the cache would never be warm"},
		{"Account balance", "NO", "-", "a stale balance is a money bug"},
		{"Stock level at checkout", "NO", "-", "read the truth before promising it"},
		{"Anything after a write, in the same request", "NO", "-", "read-your-own-writes must come from the source"},
	}

	fmt.Printf("  %-38s %-9s %-10s %s\n", "DATA", "CACHE?", "TTL", "WHY")

	for _, row := range rows {
		fmt.Printf("  %-38s %-9s %-10s %s\n", row.data, row.cache, row.ttl, row.why)
	}

	fmt.Println("\n  The test is not \"is it slow?\" - it is \"can this be a few seconds")
	fmt.Println("  out of date without anybody being harmed?\"")
}

func printPitfalls() {
	fmt.Println("\n5) The four ways caching goes wrong")
	fmt.Println(strings.Repeat("-", 78))

	fmt.Println(`  Stampede (thundering herd)
    A hot key expires and 500 concurrent requests all miss and all hit the
    database at once - which is when the database falls over.
    Fixes: singleflight (one loader per key), a randomised TTL, or refreshing
    slightly before expiry.

  Invalidate-then-write
    Deleting the key BEFORE writing the database leaves a window where a
    reader repopulates the cache with the old value, which then survives a
    whole TTL. Write first, invalidate second - as UpdatePrice does.

  Caching per instance
    An in-process cache on three replicas is three caches. An invalidation on
    one does nothing to the other two. That is exactly when Redis stops being
    optional.

  Unbounded keys
    Caching by a user-controlled key (a search string, a URL) with no limit
    fills memory with entries nobody will read again. Bound the key space, or
    bound the memory with an eviction policy (Redis: maxmemory-policy
    allkeys-lru).`)

	fmt.Println()
}
