package webui

import (
	"testing"
	"time"
)

func TestLoginThrottleDelaysAfterThreshold(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		if delay := throttle.Delay("1.2.3.4"); delay != 0 {
			t.Fatalf("attempt %d: expected no delay yet, got %v", i, delay)
		}
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("1.2.3.4"); delay != 2*time.Second {
		t.Fatalf("expected 2s delay after 5 failures, got %v", delay)
	}
}

func TestLoginThrottleIsKeyedIndependently(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("5.6.7.8"); delay != 0 {
		t.Fatalf("expected a different key to be unaffected, got %v", delay)
	}
}

func TestLoginThrottleResetsOnSuccess(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	throttle.Reset("1.2.3.4")
	if delay := throttle.Delay("1.2.3.4"); delay != 0 {
		t.Fatalf("expected reset to clear the delay, got %v", delay)
	}
}

// The penalty is a cooling-off period per attempt, not a lockout for the rest
// of the window: an attacker pays throttleDelay before each further guess,
// while an owner who mistyped five times waits seconds rather than minutes.
func TestLoginThrottleDelayExpiresBetweenAttempts(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	now = now.Add(time.Second)
	if delay := throttle.Delay("1.2.3.4"); delay != time.Second {
		t.Fatalf("expected the remaining 1s of the penalty, got %v", delay)
	}
	now = now.Add(time.Second)
	if delay := throttle.Delay("1.2.3.4"); delay != 0 {
		t.Fatalf("expected the penalty to elapse, got %v", delay)
	}
	// Guessing again restarts the penalty, so each attempt keeps costing.
	throttle.RecordFailure("1.2.3.4")
	if delay := throttle.Delay("1.2.3.4"); delay != 2*time.Second {
		t.Fatalf("expected a fresh penalty after another failure, got %v", delay)
	}
}

func TestLoginThrottleWindowExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	throttle := NewLoginThrottle(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		throttle.RecordFailure("1.2.3.4")
	}
	if delay := throttle.Delay("1.2.3.4"); delay != 2*time.Second {
		t.Fatalf("expected delay before window expiry, got %v", delay)
	}
	now = now.Add(16 * time.Minute)
	if delay := throttle.Delay("1.2.3.4"); delay != 0 {
		t.Fatalf("expected delay to clear after the window expires, got %v", delay)
	}
}
