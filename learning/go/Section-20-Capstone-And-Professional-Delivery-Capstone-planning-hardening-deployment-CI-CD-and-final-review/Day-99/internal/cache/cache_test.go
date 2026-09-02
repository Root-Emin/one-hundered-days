package cache_test

import (
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/cache"
	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-99/internal/domain"
)

func newLink(code string) domain.Link {
	return domain.Link{Code: domain.Code(code), Target: "https://example.com", Active: true}
}

func TestHitAndMiss(t *testing.T) {
	c := cache.New(cache.DefaultOptions(time.Minute))

	if _, _, hit := c.Get("absent"); hit {
		t.Error("an empty cache reported a hit")
	}

	c.Put(newLink("golang"))

	link, found, hit := c.Get("golang")

	if !hit || !found {
		t.Fatalf("hit = %t, found = %t, want both true", hit, found)
	}

	if link.Target != "https://example.com" {
		t.Errorf("target = %q", link.Target)
	}
}

// The negative entry is what stops a crawler hammering an unknown code from
// reaching the database on every request.
func TestNegativeCaching(t *testing.T) {
	c := cache.New(cache.DefaultOptions(time.Minute))

	c.PutMissing("nosuch")

	_, found, hit := c.Get("nosuch")

	if !hit {
		t.Fatal("the negative entry was not a hit")
	}

	if found {
		t.Error("a negative entry reported the link as found")
	}
}

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	c := cache.New(cache.Options{TTL: time.Minute, NegativeTTL: 10 * time.Second, MaxEntries: 100})
	c.SetClock(func() time.Time { return now })

	c.Put(newLink("golang"))
	c.PutMissing("nosuch")

	// The negative entry expires first, on purpose: a link that appears should
	// start working quickly.
	now = now.Add(30 * time.Second)

	if _, _, hit := c.Get("nosuch"); hit {
		t.Error("the negative entry outlived its shorter TTL")
	}

	if _, _, hit := c.Get("golang"); !hit {
		t.Error("the positive entry expired too early")
	}

	now = now.Add(time.Minute)

	if _, _, hit := c.Get("golang"); hit {
		t.Error("the positive entry outlived its TTL")
	}
}

func TestInvalidate(t *testing.T) {
	c := cache.New(cache.DefaultOptions(time.Minute))

	c.Put(newLink("golang"))

	c.Invalidate("golang")

	if _, _, hit := c.Get("golang"); hit {
		t.Error("the entry survived invalidation - a deactivated link would keep redirecting")
	}
}

// Unbounded, this is a memory leak with a friendly name.
func TestEvictionBoundsMemory(t *testing.T) {
	c := cache.New(cache.Options{TTL: time.Hour, NegativeTTL: time.Minute, MaxEntries: 50})

	for i := 0; i < 500; i++ {
		c.Put(newLink(string(rune('a'+i%26)) + string(rune('a'+i/26))))
	}

	_, _, size := c.Stats()

	if size > 50 {
		t.Errorf("size = %d, want at most 50", size)
	}
}

func TestHitRatio(t *testing.T) {
	c := cache.New(cache.DefaultOptions(time.Minute))

	if ratio := c.HitRatio(); ratio != 0 {
		t.Errorf("an empty cache reported a ratio of %f", ratio)
	}

	c.Put(newLink("golang"))

	for i := 0; i < 9; i++ {
		c.Get("golang")
	}

	c.Get("absent")

	if ratio := c.HitRatio(); ratio < 0.89 || ratio > 0.91 {
		t.Errorf("hit ratio = %f, want ~0.9", ratio)
	}
}

func TestConcurrentUse(t *testing.T) {
	c := cache.New(cache.DefaultOptions(time.Minute))

	done := make(chan struct{})

	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			defer func() { done <- struct{}{} }()

			for i := 0; i < 200; i++ {
				code := string(rune('a' + (i+worker)%26))

				c.Put(newLink(code))
				c.Get(domain.Code(code))
				c.Invalidate(domain.Code(code))
			}
		}(worker)
	}

	for i := 0; i < 8; i++ {
		<-done
	}
}
