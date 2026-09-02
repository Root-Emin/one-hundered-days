package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/auth"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/ratelimit"
)

func TestBurstThenRefill(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	limiter := ratelimit.New(60) // one per second, burst 6
	limiter.SetClock(func() time.Time { return now })

	allowed := 0

	for i := 0; i < 20; i++ {
		if limiter.Allow("ada") {
			allowed++
		}
	}

	if allowed != 6 {
		t.Errorf("allowed %d of 20 instantly, want the burst of 6", allowed)
	}

	// A token bucket refills continuously; a fixed window would allow the
	// whole budget again at the boundary.
	now = now.Add(3 * time.Second)

	refilled := 0

	for i := 0; i < 10; i++ {
		if limiter.Allow("ada") {
			refilled++
		}
	}

	if refilled != 3 {
		t.Errorf("allowed %d after 3 seconds, want 3", refilled)
	}
}

// One client's retry loop must not become everyone else's outage.
func TestKeysAreIndependent(t *testing.T) {
	limiter := ratelimit.New(60)

	for i := 0; i < 20; i++ {
		limiter.Allow("noisy")
	}

	if !limiter.Allow("quiet") {
		t.Error("one key exhausting its bucket blocked another")
	}
}

// Without a sweep the map is an unbounded leak keyed by whatever arrives.
func TestIdleBucketsAreSweptAway(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	limiter := ratelimit.New(60)
	limiter.SetClock(func() time.Time { return now })

	for i := 0; i < 100; i++ {
		limiter.Allow(string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}

	before := limiter.Size()

	if before < 50 {
		t.Fatalf("size = %d, want the buckets to exist before the sweep", before)
	}

	// Past the idle window, then one request to trigger the sweep.
	now = now.Add(3 * time.Hour)

	limiter.Allow("fresh")

	if after := limiter.Size(); after > 2 {
		t.Errorf("size after the sweep = %d, want the idle buckets gone", after)
	}
}

func TestMiddlewareRejectsOverTheLimit(t *testing.T) {
	limiter := ratelimit.New(60)

	limited := 0

	handler := limiter.Middleware(func(w http.ResponseWriter, _ *http.Request) {
		limited++

		w.WriteHeader(http.StatusTooManyRequests)
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	statuses := make(map[int]int)

	for i := 0; i < 20; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/links", nil)
		request = request.WithContext(auth.WithOwner(request.Context(), "ada"))

		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		statuses[recorder.Code]++

		if recorder.Code == http.StatusTooManyRequests {
			// Without Retry-After a client retries immediately, and the
			// limiter becomes a busy loop for both sides.
			if recorder.Header().Get("Retry-After") == "" {
				t.Error("no Retry-After on a 429")
			}
		}
	}

	if statuses[http.StatusOK] != 6 {
		t.Errorf("allowed %d, want the burst of 6", statuses[http.StatusOK])
	}

	if statuses[http.StatusTooManyRequests] != 14 {
		t.Errorf("limited %d, want 14", statuses[http.StatusTooManyRequests])
	}
}

// The limiter keys by the authenticated owner, so it has to run after auth.
func TestUnauthenticatedRequestsPassThrough(t *testing.T) {
	limiter := ratelimit.New(1)

	reached := 0

	handler := limiter.Middleware(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++

		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/links", nil))
	}

	if reached != 5 {
		t.Errorf("reached = %d, want 5: with no owner there is no bucket to charge", reached)
	}
}
