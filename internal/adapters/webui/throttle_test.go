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
