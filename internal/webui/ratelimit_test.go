package webui

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsInitialBurst(t *testing.T) {
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	rl := newRateLimiter(5, 5, time.Minute)
	rl.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !rl.consume("1.2.3.4") {
			t.Fatalf("attempt %d: expected allow", i+1)
		}
	}
	if rl.consume("1.2.3.4") {
		t.Fatal("6th attempt should be denied")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	rl := newRateLimiter(5, 5, time.Minute)
	rl.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		rl.consume("ip")
	}
	if rl.consume("ip") {
		t.Fatal("bucket should be empty")
	}
	// Advance 60 seconds → 5 tokens refilled.
	now = now.Add(time.Minute)
	if !rl.consume("ip") {
		t.Fatal("expected refilled allow")
	}
}

func TestRateLimiterPerKeyIsolation(t *testing.T) {
	now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	rl := newRateLimiter(2, 2, time.Minute)
	rl.now = func() time.Time { return now }
	rl.consume("a")
	rl.consume("a")
	if rl.consume("a") {
		t.Fatal("a should be exhausted")
	}
	if !rl.consume("b") {
		t.Fatal("b should still be allowed")
	}
}

func TestRateLimiterGCEvictsIdle(t *testing.T) {
	now := time.Now()
	rl := newRateLimiter(5, 5, time.Minute)
	rl.now = func() time.Time { return now }

	rl.consume("10.0.0.1")
	rl.consume("10.0.0.2")

	// Advance past the idle TTL for both buckets, then touch one.
	now = now.Add(2 * time.Hour)
	rl.consume("10.0.0.1")

	rl.gc(time.Hour)

	rl.mu.Lock()
	defer rl.mu.Unlock()
	if _, ok := rl.buckets["10.0.0.2"]; ok {
		t.Error("idle bucket 10.0.0.2 should have been evicted")
	}
	if _, ok := rl.buckets["10.0.0.1"]; !ok {
		t.Error("recently-active bucket 10.0.0.1 should have been kept")
	}
}
