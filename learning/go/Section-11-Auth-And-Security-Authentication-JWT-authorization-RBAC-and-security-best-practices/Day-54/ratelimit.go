package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

/*
Rate limiting.

Two limits, because they defend different things:

	global per client   protects capacity: one caller cannot eat the service
	login per identity  protects accounts: brute force gets expensive fast

A token bucket (golang.org/x/time/rate) allows a burst and then settles to a
steady rate, which matches how real clients behave - a page load fires several
requests at once and then goes quiet.

This is an in-process limiter. Behind more than one replica it only limits per
instance; the shared version lives in Redis (Day 81) or at the edge. Say that
out loud in review rather than assuming the limiter is stronger than it is.
*/

type Limiter struct {
	rate  rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewLimiter builds a limiter of `perSecond` requests with the given burst.
func NewLimiter(perSecond float64, burst int, ttl time.Duration) *Limiter {
	return &Limiter{
		rate:    rate.Limit(perSecond),
		burst:   burst,
		ttl:     ttl,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether this key may proceed, and how long to wait if not.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, found := l.buckets[key]
	if !found {
		entry = &bucket{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[key] = entry
	}

	entry.lastSeen = time.Now()

	reservation := entry.limiter.Reserve()
	if !reservation.OK() {
		return false, time.Second
	}

	delay := reservation.Delay()

	if delay == 0 {
		return true, 0
	}

	// Not allowed now: give the token back so a rejected request does not
	// also consume future capacity.
	reservation.Cancel()

	return false, delay
}

// Cleanup drops buckets nobody has used recently. Without it the map is an
// unbounded memory leak keyed by attacker-controlled input - the limiter
// itself becomes the denial of service.
func (l *Limiter) Cleanup() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.ttl)
	removed := 0

	for key, entry := range l.buckets {
		if entry.lastSeen.Before(cutoff) {
			delete(l.buckets, key)

			removed++
		}
	}

	return removed
}

func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.buckets)
}

// RunCleanup evicts stale buckets on an interval until stop is closed.
func (l *Limiter) RunCleanup(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return

		case <-ticker.C:
			if removed := l.Cleanup(); removed > 0 {
				log.Printf("rate limiter: evicted %d idle bucket(s), %d remaining", removed, l.Size())
			}
		}
	}
}

//
// MIDDLEWARE
//

// KeyFunc decides what a limit is counted against: an IP for anonymous
// traffic, a user id or API key for authenticated traffic (fairer, and it
// survives NAT where thousands of users share one address).
type KeyFunc func(*http.Request) string

func ClientIPKey(r *http.Request) string {
	return ClientIP(r)
}

// ClientIP prefers the socket address. X-Forwarded-For is trusted only when
// TRUSTED_PROXY is set, because a header a client can write is a header an
// attacker can use to get a fresh rate limit bucket per request.
func ClientIP(r *http.Request) string {
	if trustProxyHeaders {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// Left-most entry is the original client, as set by the proxy.
			if comma := indexByte(forwarded, ','); comma > 0 {
				return trimSpace(forwarded[:comma])
			}

			return trimSpace(forwarded)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func indexByte(value string, target byte) int {
	for i := range len(value) {
		if value[i] == target {
			return i
		}
	}

	return -1
}

func trimSpace(value string) string {
	start := 0
	end := len(value)

	for start < end && (value[start] == ' ' || value[start] == '\t') {
		start++
	}

	for end > start && (value[end-1] == ' ' || value[end-1] == '\t') {
		end--
	}

	return value[start:end]
}

// RateLimit returns middleware that enforces the limiter for the given key.
func RateLimit(limiter *Limiter, key KeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := key(r)

			allowed, retryAfter := limiter.Allow(identity)
			if !allowed {
				// Retry-After tells a well-behaved client when to come back,
				// instead of leaving it to hammer the endpoint.
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))

				log.Printf("rate limited key=%s path=%s retry_after=%s",
					EscapeForLog(identity), r.URL.Path, retryAfter.Round(time.Millisecond))

				writeError(w, http.StatusTooManyRequests, "too many requests")

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LoginKey counts login attempts per submitted email *and* per IP. An
// attacker spraying one password across many accounts is invisible to a
// per-account limit, and a distributed attack is invisible to a per-IP limit.
func LoginKey(email string, r *http.Request) string {
	return fmt.Sprintf("login:%s|%s", email, ClientIP(r))
}
