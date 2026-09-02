package bufpool_test

import (
	"sync"
	"testing"

	"example.com/onehundredday/Section-18-Performance-pprof-profiling-memory-tuning-concurrency-tuning-and-DB-HTTP-optimization/Day-87/internal/bufpool"
)

func TestGetReturnsAnEmptyBuffer(t *testing.T) {
	pool := bufpool.New()

	buffer := pool.Get()

	if len(*buffer) != 0 {
		t.Errorf("length = %d, want 0", len(*buffer))
	}

	if cap(*buffer) < bufpool.DefaultCapacity {
		t.Errorf("capacity = %d, want at least %d", cap(*buffer), bufpool.DefaultCapacity)
	}
}

// The reset happens on Get, not on trust. A buffer handed out with someone
// else's bytes still in it is how one user's data ends up in another user's
// response.
func TestBuffersComeBackEmptyEvenIfPutBackDirty(t *testing.T) {
	pool := bufpool.New()

	buffer := pool.Get()
	*buffer = append(*buffer, "secret"...)

	pool.Put(buffer)

	for i := 0; i < 20; i++ {
		next := pool.Get()

		if len(*next) != 0 {
			t.Fatalf("buffer %d came back with %d bytes still in it", i, len(*next))
		}

		pool.Put(next)
	}
}

// One huge request must not permanently raise the memory floor.
func TestOversizedBuffersAreDropped(t *testing.T) {
	pool := bufpool.New()

	buffer := pool.Get()
	*buffer = make([]byte, 0, bufpool.MaxRetained*2)

	pool.Put(buffer)

	_, _, discards := pool.Stats()

	if discards != 1 {
		t.Errorf("discards = %d, want 1", discards)
	}

	// And a normal buffer still goes back.
	normal := pool.Get()
	pool.Put(normal)

	if _, _, discards = pool.Stats(); discards != 1 {
		t.Errorf("discards = %d after a normal Put, want 1", discards)
	}
}

func TestPutIgnoresNil(t *testing.T) {
	pool := bufpool.New()

	pool.Put(nil) // must not panic

	if _, _, discards := pool.Stats(); discards != 0 {
		t.Errorf("discards = %d, want 0", discards)
	}
}

// The reason the pool exists: under repeated use it creates far fewer buffers
// than it hands out.
func TestPoolReusesBuffers(t *testing.T) {
	pool := bufpool.New()

	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 200; j++ {
				buffer := pool.Get()
				*buffer = append(*buffer, "some work"...)

				pool.Put(buffer)
			}
		}()
	}

	wg.Wait()

	gets, allocations, _ := pool.Stats()

	if gets != 1600 {
		t.Errorf("gets = %d, want 1600", gets)
	}

	// Deliberately loose. sync.Pool keeps a per-P free list, so with
	// GOMAXPROCS processors the first goroutine scheduled on each P allocates
	// its own buffer, work stealing moves buffers between them, and any GC
	// cycle empties the pool entirely. The honest claim is "far fewer buffers
	// than gets", not a fixed ratio - a tighter bound here would be a flaky
	// test, which is worse than a loose one.
	if allocations > gets/2 {
		t.Errorf("allocated %d buffers for %d gets - reuse is not happening", allocations, gets)
	}
}

// A pooled Get/Put round trip should not allocate once the pool is warm.
func TestWarmPoolDoesNotAllocate(t *testing.T) {
	pool := bufpool.New()

	// Warm it.
	for i := 0; i < 4; i++ {
		pool.Put(pool.Get())
	}

	allocations := testing.AllocsPerRun(500, func() {
		buffer := pool.Get()
		*buffer = append(*buffer, "payload"...)

		pool.Put(buffer)
	})

	if allocations != 0 {
		t.Errorf("warm pool allocated %.1f times per round trip, want 0", allocations)
	}
}
