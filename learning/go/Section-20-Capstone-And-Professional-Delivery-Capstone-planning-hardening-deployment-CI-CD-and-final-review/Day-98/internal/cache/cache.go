// Package cache holds resolved links in memory, in front of the redirect's
// database read.
//
// The redirect is the only endpoint a human waits for, and the only one whose
// failure is visible to someone who never signed up. Two properties follow:
//
//   - the TTL is the bound on staleness. A deactivated link keeps redirecting
//     for up to one TTL unless something invalidates it, so the write path
//     invalidates explicitly and the TTL is the backstop for what it missed.
//   - a cache miss must never be an error. A cache that can fail the request
//     is worse than no cache.
package cache

import (
	"sync"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/domain"
)

// entry is a cached link with its expiry.
//
// A NEGATIVE entry - found=false - is cached too, and deliberately: a crawler
// hammering a code that does not exist would otherwise reach the database on
// every request, which is exactly the shape of a cheap denial of service.
type entry struct {
	link      domain.Link
	found     bool
	expiresAt time.Time
}

// Cache is a TTL map of resolved links.
type Cache struct {
	mu      sync.RWMutex
	entries map[domain.Code]entry

	ttl         time.Duration
	negativeTTL time.Duration
	maxEntries  int

	hits   int64
	misses int64

	now func() time.Time
}

// Options configure a Cache.
type Options struct {
	// TTL bounds how stale a positive entry may be.
	TTL time.Duration
	// NegativeTTL bounds a cached "not found". Shorter than TTL, because a
	// link that appears should start working quickly.
	NegativeTTL time.Duration
	// MaxEntries bounds memory. Unbounded, this is a memory leak with a
	// friendly name.
	MaxEntries int
}

// DefaultOptions are reasonable values for a redirect cache.
func DefaultOptions(ttl time.Duration) Options {
	if ttl <= 0 {
		ttl = time.Minute
	}

	return Options{TTL: ttl, NegativeTTL: 10 * time.Second, MaxEntries: 10_000}
}

// New builds a Cache.
func New(options Options) *Cache {
	if options.TTL <= 0 {
		options.TTL = time.Minute
	}

	if options.NegativeTTL <= 0 {
		options.NegativeTTL = 10 * time.Second
	}

	if options.MaxEntries <= 0 {
		options.MaxEntries = 10_000
	}

	return &Cache{
		entries:     make(map[domain.Code]entry),
		ttl:         options.TTL,
		negativeTTL: options.NegativeTTL,
		maxEntries:  options.MaxEntries,
		now:         time.Now,
	}
}

// SetClock replaces the time source, so expiry can be tested without sleeping.
func (c *Cache) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = now
}

// Get returns a cached link.
//
// The three-valued result is the point: (link, true, true) is a hit for a link
// that exists, (_, false, true) is a hit for one that is known NOT to exist,
// and (_, _, false) is a miss that the caller must resolve.
func (c *Cache) Get(code domain.Code) (link domain.Link, found bool, hit bool) {
	c.mu.RLock()

	cached, present := c.entries[code]
	now := c.now()

	c.mu.RUnlock()

	if !present || now.After(cached.expiresAt) {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()

		return domain.Link{}, false, false
	}

	c.mu.Lock()
	c.hits++
	c.mu.Unlock()

	return cached.link, cached.found, true
}

// Put caches a link that exists.
func (c *Cache) Put(link domain.Link) {
	c.store(link.Code, entry{link: link, found: true, expiresAt: c.now().Add(c.ttl)})
}

// PutMissing caches the absence of a code.
func (c *Cache) PutMissing(code domain.Code) {
	c.store(code, entry{found: false, expiresAt: c.now().Add(c.negativeTTL)})
}

// Invalidate drops an entry.
//
// Called by the write path AFTER the database commits. Deleting first leaves a
// window where a concurrent reader repopulates the cache from the old row, and
// the stale value then survives a full TTL.
func (c *Cache) Invalidate(code domain.Code) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, code)
}

func (c *Cache) store(code domain.Code, value entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}

	c.entries[code] = value
}

// evictLocked makes room.
//
// It drops expired entries first, and if that is not enough, an arbitrary
// handful. Not LRU: tracking recency costs a write on every read, which is the
// opposite of what a read cache is for. For a redirect workload the popular
// codes are re-cached on their next request anyway.
func (c *Cache) evictLocked() {
	now := c.now()

	for code, cached := range c.entries {
		if now.After(cached.expiresAt) {
			delete(c.entries, code)
		}
	}

	if len(c.entries) < c.maxEntries {
		return
	}

	target := c.maxEntries / 10

	for code := range c.entries {
		delete(c.entries, code)

		target--

		if target <= 0 {
			return
		}
	}
}

// Stats reports hits, misses and size.
//
// The hit ratio is the number that says whether the cache is earning its
// complexity: near zero means you are paying for a cache and getting a second
// lookup.
func (c *Cache) Stats() (hits, misses int64, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.hits, c.misses, len(c.entries)
}

// HitRatio returns hits / (hits + misses).
func (c *Cache) HitRatio() float64 {
	hits, misses, _ := c.Stats()

	total := hits + misses

	if total == 0 {
		return 0
	}

	return float64(hits) / float64(total)
}
