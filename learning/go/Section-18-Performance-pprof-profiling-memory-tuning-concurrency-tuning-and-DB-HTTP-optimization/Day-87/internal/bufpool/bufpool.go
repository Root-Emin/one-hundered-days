// Package bufpool is a sync.Pool of byte buffers, with the guards that make
// pooling safe rather than a source of subtle bugs.
//
// Read this before reaching for sync.Pool:
//
//   - A pool is not a cache. Its contents are cleared at every GC cycle, so it
//     helps with short-lived churn inside a request and does nothing for
//     anything you hoped to keep.
//   - A pooled buffer that is not reset leaks the previous request's data into
//     the next one. In a response body, that is a security incident, not a
//     performance bug.
//   - Putting back an enormous buffer keeps that memory alive forever. One
//     10 MB request permanently raises the floor of every pooled buffer.
//   - Pooling only pays when the profile shows churn. Below a few hundred
//     bytes, allocation is a pointer bump and the pool's own overhead
//     (an interface conversion, atomic operations, a per-P cache lookup) can
//     cost more than it saves. Measure both ways.
package bufpool

import (
	"sync"
	"sync/atomic"
)

// MaxRetained caps what goes back into the pool. Buffers larger than this are
// dropped for the GC to reclaim.
const MaxRetained = 64 << 10 // 64 KiB

// DefaultCapacity is what a fresh buffer starts with, sized so a typical
// request never has to grow it.
const DefaultCapacity = 4 << 10

// Pool hands out byte slices and takes them back.
type Pool struct {
	pool sync.Pool

	gets     atomic.Int64
	news     atomic.Int64
	discards atomic.Int64
}

func New() *Pool {
	p := &Pool{}

	p.pool.New = func() any {
		p.news.Add(1)

		// A pointer to a slice, not a slice. Putting a slice into an `any`
		// allocates a header on every Put - the exact cost the pool exists to
		// avoid. go vet's SA6002 flags this.
		buffer := make([]byte, 0, DefaultCapacity)

		return &buffer
	}

	return p
}

// Get returns an empty buffer with whatever capacity it already had.
func (p *Pool) Get() *[]byte {
	p.gets.Add(1)

	buffer, ok := p.pool.Get().(*[]byte)
	if !ok {
		// Cannot happen with the New above, but a type assertion that can
		// panic in a hot path is not worth the two lines it saves.
		fresh := make([]byte, 0, DefaultCapacity)

		return &fresh
	}

	// Reset here rather than trusting the caller to have done it: the failure
	// mode of a forgotten reset is one user seeing another user's data.
	*buffer = (*buffer)[:0]

	return buffer
}

// Put returns a buffer to the pool unless it has grown too large.
func (p *Pool) Put(buffer *[]byte) {
	if buffer == nil {
		return
	}

	if cap(*buffer) > MaxRetained {
		// Dropping it is the point: one huge request must not permanently
		// raise the memory floor of every future request.
		p.discards.Add(1)

		return
	}

	*buffer = (*buffer)[:0]

	p.pool.Put(buffer)
}

// Stats reports how often the pool actually reused something.
//
// news/gets is the miss rate. A miss rate near 1 means the pool is not helping
// - usually because the objects live too long, or because a GC cycle empties
// it between uses.
func (p *Pool) Stats() (gets, allocations, discards int64) {
	return p.gets.Load(), p.news.Load(), p.discards.Load()
}
