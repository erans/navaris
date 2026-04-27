package api

import (
	"testing"
	"time"
)

func TestRateLimiter_BurstThenWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	r := NewRateLimiter(RateLimiterConfig{Burst: 3, RefillPerSec: 1.0, IdleTTL: time.Hour}, clock)

	for i := 0; i < 3; i++ {
		if !r.Allow("sbx-1") {
			t.Fatalf("burst attempt %d denied", i)
		}
	}
	if r.Allow("sbx-1") {
		t.Fatal("4th attempt should be denied")
	}

	// Refill: advance by 2 seconds → 2 tokens.
	now = now.Add(2 * time.Second)
	for i := 0; i < 2; i++ {
		if !r.Allow("sbx-1") {
			t.Fatalf("after refill attempt %d denied", i)
		}
	}
	if r.Allow("sbx-1") {
		t.Fatal("3rd attempt after refill should be denied")
	}
}

func TestRateLimiter_PerSandboxIsolation(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 1.0, IdleTTL: time.Hour}, func() time.Time { return now })

	if !r.Allow("a") {
		t.Fatal("a denied first attempt")
	}
	if r.Allow("a") {
		t.Fatal("a allowed second attempt within burst")
	}
	if !r.Allow("b") {
		t.Fatal("b denied first attempt — buckets should be per-sandbox")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := NewRateLimiter(RateLimiterConfig{Burst: 1, RefillPerSec: 0.5, IdleTTL: time.Hour}, func() time.Time { return now })

	r.Allow("sbx-1")              // consume token
	wait := r.RetryAfter("sbx-1") // empty bucket; refill is 0.5/s → ~2s for next token
	if wait < 1900*time.Millisecond || wait > 2100*time.Millisecond {
		t.Errorf("retryAfter = %v, want ~2s", wait)
	}
}
