package api

import (
	"math"
	"sync"
	"time"
)

// RateLimiterConfig is exported so cmd/navarisd/main.go can construct one
// without importing internal types.
type RateLimiterConfig struct {
	Burst        int
	RefillPerSec float64
	IdleTTL      time.Duration
}

type RateLimiter struct {
	cfg RateLimiterConfig
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens   float64
	lastFill time.Time
}

func NewRateLimiter(cfg RateLimiterConfig, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		cfg:     cfg,
		now:     now,
		buckets: make(map[string]*rateBucket),
	}
}

// NewRateLimiterDefault returns a limiter with the canonical boost-channel
// config: 1 rps, burst 10, 1h idle TTL.
func NewRateLimiterDefault() *RateLimiter {
	return NewRateLimiter(RateLimiterConfig{Burst: 10, RefillPerSec: 1.0, IdleTTL: time.Hour}, nil)
}

// Allow takes one token from the sandbox's bucket. Returns true on success
// (token taken), false on bucket-empty.
func (r *RateLimiter) Allow(sandboxID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	b, ok := r.buckets[sandboxID]
	if !ok {
		b = &rateBucket{tokens: float64(r.cfg.Burst), lastFill: now}
		r.buckets[sandboxID] = b
	} else {
		elapsed := now.Sub(b.lastFill).Seconds()
		b.tokens = math.Min(float64(r.cfg.Burst), b.tokens+elapsed*r.cfg.RefillPerSec)
		b.lastFill = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

// RetryAfter returns the duration until the next token is available. Only
// meaningful immediately after a denied Allow().
func (r *RateLimiter) RetryAfter(sandboxID string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.buckets[sandboxID]
	if !ok || b.tokens >= 1.0 {
		return 0
	}
	missing := 1.0 - b.tokens
	secs := missing / r.cfg.RefillPerSec
	return time.Duration(secs * float64(time.Second))
}

// GC drops buckets idle longer than IdleTTL. Call from a periodic loop.
func (r *RateLimiter) GC() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-r.cfg.IdleTTL)
	for k, b := range r.buckets {
		if b.lastFill.Before(cutoff) {
			delete(r.buckets, k)
		}
	}
}
