// Package cache is the MVP's read cache: an in-process TTL map behind a small
// interface, so the same code runs against Redis in production.
//
// Two rules the API depends on:
//
//   - TTL is the bound on staleness, not the invalidation strategy. It is the
//     backstop for the invalidation you forgot.
//   - Invalidation happens AFTER the database write commits. Deleting first
//     leaves a window where a concurrent reader repopulates the cache from the
//     old row and the stale value survives the update.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrMiss is returned for a key that is absent or expired. It is not a
// failure - it is the normal path half the time.
var ErrMiss = errors.New("cache miss")

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

// Memory is a Cache for tests and single-process deployments.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry

	hits   atomic.Int64
	misses atomic.Int64

	// now is injectable so a test can expire an entry without sleeping.
	now func() time.Time
}

func NewMemory() *Memory {
	return &Memory{entries: make(map[string]entry), now: time.Now}
}

// SetClock lets a test control expiry.
func (m *Memory) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.now = now
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()

	found, ok := m.entries[key]
	expired := ok && m.now().After(found.expiresAt)

	m.mu.RUnlock()

	if !ok || expired {
		m.misses.Add(1)

		return nil, ErrMiss
	}

	m.hits.Add(1)

	return append([]byte(nil), found.value...), nil
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[key] = entry{value: append([]byte(nil), value...), expiresAt: m.now().Add(ttl)}

	return nil
}

func (m *Memory) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		delete(m.entries, key)
	}

	return nil
}

// Stats is what tells you whether the cache is worth its complexity. A hit
// rate near zero means you are paying for a cache and getting a second
// database call.
func (m *Memory) Stats() (hits, misses int64) {
	return m.hits.Load(), m.misses.Load()
}

func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.entries)
}

// GetJSON and SetJSON keep encoding in one place instead of at every call site.
func GetJSON[T any](ctx context.Context, c Cache, key string) (T, error) {
	var value T

	raw, err := c.Get(ctx, key)
	if err != nil {
		return value, err
	}

	if err := json.Unmarshal(raw, &value); err != nil {
		// A corrupt entry is a miss, not an outage: drop it and read through.
		if deleteErr := c.Delete(ctx, key); deleteErr != nil {
			return value, fmt.Errorf("decode %s: %w (delete failed: %v)", key, err, deleteErr)
		}

		return value, ErrMiss
	}

	return value, nil
}

func SetJSON(ctx context.Context, c Cache, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}

	return c.Set(ctx, key, raw, ttl)
}

// ProductKey keeps key construction in one place. Keys built inline drift, and
// then the invalidation deletes a key nobody wrote.
func ProductKey(id int64) string {
	return fmt.Sprintf("product:%d", id)
}
