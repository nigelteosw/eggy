package webui

import (
	"testing"
	"time"
)

func TestSignSessionRoundTrips(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(time.Hour))
	if !VerifySession(key, token, now) {
		t.Fatal("expected valid, unexpired token to verify")
	}
}

func TestVerifySessionRejectsExpiredToken(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(-time.Second))
	if VerifySession(key, token, now) {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestVerifySessionRejectsTamperedToken(t *testing.T) {
	key := []byte("test-key")
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession(key, now.Add(time.Hour))
	tampered := token[:len(token)-1] + "0"
	if VerifySession(key, tampered, now) {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestVerifySessionRejectsWrongKey(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	token := SignSession([]byte("key-a"), now.Add(time.Hour))
	if VerifySession([]byte("key-b"), token, now) {
		t.Fatal("expected token signed with a different key to be rejected")
	}
}

func TestVerifySessionRejectsMalformedToken(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, token := range []string{"", "no-dot-here", "not-a-number.deadbeef", "123.not-hex"} {
		if VerifySession([]byte("key"), token, now) {
			t.Fatalf("expected malformed token %q to be rejected", token)
		}
	}
}
