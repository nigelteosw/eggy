package services

import (
	"context"
	"sync"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
)

// ActiveTurns tracks the turn currently running in each conversation, so
// /stop can cancel it. With one loop there is no separate "run" to
// interrupt: a long editing turn *is* the turn, and cancelling its context
// is what stopping means.
//
// Cancellation only stops the loop from taking further steps. The thread's
// workspace and its recorded session survive, so a stopped turn leaves the
// checkout inspectable and the change resumable by simply saying so.
type ActiveTurns struct {
	mu     sync.Mutex
	next   uint64
	active map[string]activeTurn
}

type activeTurn struct {
	id     uint64
	cancel context.CancelFunc
}

func NewActiveTurns() *ActiveTurns {
	return &ActiveTurns{active: map[string]activeTurn{}}
}

// Begin derives a cancellable context for the turn ctx belongs to and
// registers it. The returned release function must be called when the turn
// ends; it cancels and deregisters, and is safe to call more than once.
func (t *ActiveTurns) Begin(ctx context.Context) (context.Context, func()) {
	conversation := destination.FromContext(ctx).ConversationID()
	turnContext, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.next++
	turn := activeTurn{id: t.next, cancel: cancel}
	// A second turn in the same conversation replaces the first as the
	// cancellation target; the first still cancels through its own release.
	t.active[conversation] = turn
	t.mu.Unlock()
	return turnContext, func() {
		t.mu.Lock()
		// Only deregister if this turn is still the registered one, so a
		// finishing turn never clears a newer turn's cancellation.
		if current, ok := t.active[conversation]; ok && current.id == turn.id {
			delete(t.active, conversation)
		}
		t.mu.Unlock()
		cancel()
	}
}

// Stop cancels the turn running in ctx's conversation, reporting whether
// there was one.
func (t *ActiveTurns) Stop(ctx context.Context) bool {
	conversation := destination.FromContext(ctx).ConversationID()
	t.mu.Lock()
	turn, ok := t.active[conversation]
	delete(t.active, conversation)
	t.mu.Unlock()
	if !ok {
		return false
	}
	turn.cancel()
	return true
}

// Active reports whether any conversation currently has a turn running. The
// heartbeat uses it to stay out of the way of live work.
func (t *ActiveTurns) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active) > 0
}
