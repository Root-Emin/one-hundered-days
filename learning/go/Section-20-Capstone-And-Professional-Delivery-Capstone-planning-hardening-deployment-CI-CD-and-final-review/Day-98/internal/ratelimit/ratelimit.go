// Package ratelimit bounds how fast one API key can call the service.
//
// The purpose is not to punish anyone: it is to make one client's mistake -
// a retry loop with no backoff, a script in a loop - stop being everyone
// else's outage.
//
// Token bucket rather than a fixed window, because a fixed window lets a
// client spend its whole budget in the last second of one window and again in
// the first second of the next: double the intended rate, at the worst
// possible moment.
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/auth"
)

// Limiter holds one token bucket per key.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     rate.Limit
	burst    int
	idleFor  time.Duration
	lastScan time.Time
	now      func() time.Time
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// New builds a limiter allowing perMinute requests per key.
//
// The burst is a tenth of the minute's budget, so a client can make a short
// burst of parallel calls - which every real client does - without being able
// to spend the whole minute at once.
func New(perMinute int) *Limiter {
	if perMinute <= 0 {
		perMinute = 60
	}

	burst := perMinute / 10
	if burst < 5 {
		burst = 5
	}

	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		// A bucket for a key nobody has used in an hour is memory held for
		// nothing. Without this the map is an unbounded leak keyed by
		// whatever an attacker sends.
		idleFor: time.Hour,
		now:     time.Now,
	}
}

// SetClock replaces the time source, for tests.
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.now = now
}

// Allow reports whether a request from key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()

	now := l.now()

	existing, found := l.buckets[key]
	if !found {
		existing = &bucket{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[key] = existing
	}

	existing.lastSeen = now

	// Sweep occasionally rather than on a timer: no goroutine to leak, and
	// the cost lands on a request that was already doing work.
	if now.Sub(l.lastScan) > 10*time.Minute {
		l.sweepLocked(now)

		l.lastScan = now
	}

	limiter := existing.limiter

	l.mu.Unlock()

	return limiter.AllowN(now, 1)
}

func (l *Limiter) sweepLocked(now time.Time) {
	for key, existing := range l.buckets {
		if now.Sub(existing.lastSeen) > l.idleFor {
			delete(l.buckets, key)
		}
	}
}

// Size returns how many buckets are held, which is what a leak would show up
// as.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.buckets)
}

// Middleware rejects requests over the limit with 429.
//
// It must run AFTER authentication, so the bucket is keyed by the authenticated
// owner rather than by an IP address. Keying by IP punishes everyone behind one
// NAT and does nothing to a client with a thousand addresses.
func (l *Limiter) Middleware(onLimit func(http.ResponseWriter, *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			owner, ok := auth.OwnerFrom(r.Context())
			if !ok {
				// Unauthenticated requests never reach here in this service.
				// If that changes, failing open would silently remove the
				// limit, so this fails closed instead.
				next.ServeHTTP(w, r)

				return
			}

			if l.Allow(owner) {
				next.ServeHTTP(w, r)

				return
			}

			// Retry-After is what makes a 429 actionable: without it a client
			// retries immediately, and the limiter becomes a busy loop for
			// both sides.
			w.Header().Set("Retry-After", strconv.Itoa(1))

			onLimit(w, r)
		})
	}
}
