package webui

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket with refill-on-consume semantics.
// Used by the /ui/login endpoint to throttle brute-force attempts.
type rateLimiter struct {
	capacity float64
	refill   float64 // tokens per refillInterval
	interval time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket

	// now is swappable for tests.
	now func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(capacity, refillPerInterval float64, interval time.Duration) *rateLimiter {
	return &rateLimiter{
		capacity: capacity,
		refill:   refillPerInterval,
		interval: interval,
		buckets:  make(map[string]*bucket),
		now:      time.Now,
	}
}

// consume tries to take one token from the bucket for key.
// Returns true if allowed, false if the bucket is empty.
func (r *rateLimiter) consume(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	b, ok := r.buckets[key]
	if !ok {
		b = &bucket{tokens: r.capacity, last: now}
		r.buckets[key] = b
	}
	elapsed := now.Sub(b.last)
	if elapsed > 0 {
		rate := r.refill / float64(r.interval)
		b.tokens += float64(elapsed) * rate
		if b.tokens > r.capacity {
			b.tokens = r.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
