package webui

import (
	"sync"
	"time"
)

const (
	throttleWindow    = 15 * time.Minute
	throttleThreshold = 5
	throttleDelay     = 2 * time.Second
)

// LoginThrottle delays repeated failed login attempts from the same key
// (typically a client IP) so casual password guessing costs real time,
// without a persistent lockout store -- state resets on process restart,
// which is acceptable for Eggy's single-owner threat model.
type LoginThrottle struct {
	mu       sync.Mutex
	now      func() time.Time
	attempts map[string]*attemptState
}

type attemptState struct {
	failures    int
	windowStart time.Time
}

func NewLoginThrottle(now func() time.Time) *LoginThrottle {
	if now == nil {
		now = time.Now
	}
	return &LoginThrottle{now: now, attempts: map[string]*attemptState{}}
}

// Delay returns how long the caller should wait before processing a login
// attempt from key.
func (t *LoginThrottle) Delay(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stateLocked(key).failures >= throttleThreshold {
		return throttleDelay
	}
	return 0
}

// RecordFailure counts one more failed attempt from key.
func (t *LoginThrottle) RecordFailure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stateLocked(key).failures++
}

// Reset clears key's failure count, e.g. after a successful login.
func (t *LoginThrottle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

func (t *LoginThrottle) stateLocked(key string) *attemptState {
	state, ok := t.attempts[key]
	if !ok || t.now().Sub(state.windowStart) > throttleWindow {
		state = &attemptState{windowStart: t.now()}
		t.attempts[key] = state
	}
	return state
}
