package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"example.com/onehundredday/Section-17-Caching-And-Messaging-Redis-caching-message-queues-event-driven-patterns-and-async-workflows/Day-81/internal/cache"
)

/*
The same suite runs against both implementations.

miniredis is a real Redis protocol implementation in Go: the client code under
test is the production client, talking the production protocol, without a
server to install. Its clock is controllable, so TTL behaviour is testable
without sleeping.
*/

func implementations(t *testing.T) map[string]cache.Cache {
	t.Helper()

	memory := cache.NewMemory(time.Minute)

	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Errorf("close memory cache: %v", err)
		}
	})

	server := miniredis.RunT(t)

	client := cache.NewRedis(cache.RedisConfig{
		Address:      server.Addr(),
		Prefix:       "test",
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		PoolSize:     4,
	})

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close redis: %v", err)
		}
	})

	return map[string]cache.Cache{"memory": memory, "redis": client}
}

func TestSetGetDelete(t *testing.T) {
	for name, backend := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			if _, err := backend.Get(ctx, "absent"); !errors.Is(err, cache.ErrMiss) {
				t.Fatalf("missing key err = %v, want ErrMiss", err)
			}

			if err := backend.Set(ctx, "key", []byte("value"), time.Minute); err != nil {
				t.Fatalf("set: %v", err)
			}

			value, err := backend.Get(ctx, "key")
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if string(value) != "value" {
				t.Fatalf("value = %q", value)
			}

			if err := backend.Delete(ctx, "key"); err != nil {
				t.Fatalf("delete: %v", err)
			}

			if _, err := backend.Get(ctx, "key"); !errors.Is(err, cache.ErrMiss) {
				t.Fatalf("after delete err = %v, want ErrMiss", err)
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type payload struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}

	for name, backend := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			if err := cache.SetJSON(ctx, backend, "item", payload{ID: 7, Name: "seven"}, time.Minute); err != nil {
				t.Fatalf("set: %v", err)
			}

			value, err := cache.GetJSON[payload](ctx, backend, "item")
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if value.ID != 7 || value.Name != "seven" {
				t.Fatalf("value = %+v", value)
			}
		})
	}
}

// TestCorruptEntryIsAMiss: a bad entry must send the caller to the source of
// truth, not fail the request.
func TestCorruptEntryIsAMiss(t *testing.T) {
	for name, backend := range implementations(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			if err := backend.Set(ctx, "broken", []byte("{not json"), time.Minute); err != nil {
				t.Fatalf("set: %v", err)
			}

			if _, err := cache.GetJSON[map[string]any](ctx, backend, "broken"); !errors.Is(err, cache.ErrMiss) {
				t.Fatalf("err = %v, want ErrMiss", err)
			}
		})
	}
}

// TestTTLExpires uses miniredis's controllable clock and the memory cache's
// real one - no sleeping in either case.
func TestTTLExpires(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		memory := cache.NewMemory(time.Hour)

		t.Cleanup(func() {
			if err := memory.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})

		ctx := context.Background()

		// A TTL already in the past.
		if err := memory.Set(ctx, "key", []byte("value"), time.Nanosecond); err != nil {
			t.Fatalf("set: %v", err)
		}

		time.Sleep(2 * time.Millisecond)

		if _, err := memory.Get(ctx, "key"); !errors.Is(err, cache.ErrMiss) {
			t.Fatalf("expired entry err = %v, want ErrMiss", err)
		}
	})

	t.Run("redis", func(t *testing.T) {
		server := miniredis.RunT(t)

		client := cache.NewRedis(cache.RedisConfig{
			Address: server.Addr(), Prefix: "test",
			DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
		})

		t.Cleanup(func() {
			if err := client.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})

		ctx := context.Background()

		if err := client.Set(ctx, "key", []byte("value"), 30*time.Second); err != nil {
			t.Fatalf("set: %v", err)
		}

		if _, err := client.Get(ctx, "key"); err != nil {
			t.Fatalf("get before expiry: %v", err)
		}

		// Move Redis's clock forward instead of waiting.
		server.FastForward(31 * time.Second)

		if _, err := client.Get(ctx, "key"); !errors.Is(err, cache.ErrMiss) {
			t.Fatalf("after expiry err = %v, want ErrMiss", err)
		}
	})
}

// TestRedisOutageIsAMiss is the resilience assertion: when Redis is gone, the
// service must get a miss and read the database, not an error.
func TestRedisOutageIsAMiss(t *testing.T) {
	server := miniredis.RunT(t)

	client := cache.NewRedis(cache.RedisConfig{
		Address: server.Addr(), Prefix: "test",
		DialTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond, PoolSize: 2,
	})

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	ctx := context.Background()

	if err := client.Set(ctx, "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}

	// The cache dies.
	server.Close()

	if _, err := client.Get(ctx, "key"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("err = %v, want ErrMiss so the caller falls back to the database", err)
	}
}

func TestKeysArePrefixed(t *testing.T) {
	server := miniredis.RunT(t)

	client := cache.NewRedis(cache.RedisConfig{
		Address: server.Addr(), Prefix: "svc",
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if err := client.Set(context.Background(), "user:1", []byte("x"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Two services sharing one Redis must not collide on "user:1".
	if !server.Exists("svc:user:1") {
		t.Fatalf("keys in redis: %v, want the prefixed form", server.Keys())
	}
}

func TestDeleteByPatternUsesScan(t *testing.T) {
	server := miniredis.RunT(t)

	client := cache.NewRedis(cache.RedisConfig{
		Address: server.Addr(), Prefix: "svc",
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})

	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	ctx := context.Background()

	for _, key := range []string{"product:1", "product:2", "product:3", "user:1"} {
		if err := client.Set(ctx, key, []byte("x"), time.Minute); err != nil {
			t.Fatalf("set: %v", err)
		}
	}

	removed, err := client.DeleteByPattern(ctx, "product:*")
	if err != nil {
		t.Fatalf("delete by pattern: %v", err)
	}

	if removed != 3 {
		t.Fatalf("removed %d keys, want 3", removed)
	}

	// The unrelated key survives: a pattern delete must not be a flush.
	if _, err := client.Get(ctx, "user:1"); err != nil {
		t.Fatalf("unrelated key was deleted: %v", err)
	}
}
