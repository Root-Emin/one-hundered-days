// Package cache holds the caching abstraction and two implementations.
//
// The interface exists so the service can run against Redis in production and
// an in-process map in a test or a single-instance deployment - and so the
// caching STRATEGY (cache-aside, TTL, invalidation) is written once.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrMiss is returned when a key is absent. A miss is not a failure: it is the
// normal case that the cache exists to make less frequent.
var ErrMiss = errors.New("cache miss")

// Cache is the seam. Deliberately small: get, set, delete, and a pattern
// delete for invalidating a group of keys.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	Close() error
}

// GetJSON is a typed helper over the byte interface.
//
// Generic functions cannot be methods, which is why this is a function rather
// than part of the interface - and it keeps the interface small enough to fake
// in twenty lines.
func GetJSON[T any](ctx context.Context, cache Cache, key string) (T, error) {
	var value T

	raw, err := cache.Get(ctx, key)
	if err != nil {
		return value, err
	}

	if err := json.Unmarshal(raw, &value); err != nil {
		// A corrupt entry is a miss, not an error: the caller reloads from the
		// source and overwrites it. Failing here would turn a bad cache entry
		// into an outage.
		return value, fmt.Errorf("%w: corrupt entry: %w", ErrMiss, err)
	}

	return value, nil
}

func SetJSON(ctx context.Context, cache Cache, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}

	return cache.Set(ctx, key, raw, ttl)
}

//
// IN-MEMORY
//

// Memory is a process-local cache: no network, no serialisation cost, and no
// sharing between instances.
//
// Right for a single instance, a sidecar-free deployment, or a test. Wrong the
// moment there are two replicas, because each one caches - and invalidates -
// only its own copy.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry

	hits   int64
	misses int64

	stop chan struct{}
	once sync.Once
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

func NewMemory(sweepInterval time.Duration) *Memory {
	memory := &Memory{
		entries: make(map[string]entry),
		stop:    make(chan struct{}),
	}

	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}

	// Expired entries are removed lazily on read AND swept periodically:
	// without the sweep, a key that is written once and never read again
	// occupies memory forever.
	go memory.sweep(sweepInterval)

	return memory
}

func (m *Memory) sweep(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return

		case now := <-ticker.C:
			m.mu.Lock()

			for key, item := range m.entries {
				if !item.expiresAt.IsZero() && now.After(item.expiresAt) {
					delete(m.entries, key)
				}
			}

			m.mu.Unlock()
		}
	}
}

func (m *Memory) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	item, found := m.entries[key]
	m.mu.RUnlock()

	if !found || (!item.expiresAt.IsZero() && time.Now().After(item.expiresAt)) {
		m.mu.Lock()
		m.misses++
		m.mu.Unlock()

		return nil, ErrMiss
	}

	m.mu.Lock()
	m.hits++
	m.mu.Unlock()

	// Return a copy: handing out the stored slice lets a caller mutate the
	// cache by accident.
	value := make([]byte, len(item.value))
	copy(value, item.value)

	return value, nil
}

func (m *Memory) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	stored := make([]byte, len(value))
	copy(stored, value)

	item := entry{value: stored}

	if ttl > 0 {
		item.expiresAt = time.Now().Add(ttl)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[key] = item

	return nil
}

func (m *Memory) Delete(ctx context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range keys {
		delete(m.entries, key)
	}

	return nil
}

func (m *Memory) Close() error {
	m.once.Do(func() { close(m.stop) })

	return nil
}

func (m *Memory) Stats() (hits, misses int64, size int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.hits, m.misses, len(m.entries)
}
