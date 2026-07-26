package services

import (
	"context"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ActiveTurns tracks the turn currently running in each conversation, so an
// owner can stop it or steer it. With one loop there is no separate "run" to
// interrupt or redirect: a long editing turn *is* the turn.
//
// Cancellation only stops the loop from taking further steps. The thread's
// workspace and its recorded session survive, so a stopped turn leaves the
// checkout inspectable and the change resumable by simply saying so.
//
// Steering is the other half: a message that arrives while a turn is running
// joins that turn at its next step boundary instead of starting a competing
// one. Only turns marked steerable accept it -- a heartbeat or scheduled turn
// is deliberately self-contained, and an owner message must never be folded
// into one.
type ActiveTurns struct {
	mu     sync.Mutex
	next   uint64
	active map[string]*activeTurn
}

type activeTurn struct {
	id        uint64
	cancel    context.CancelFunc
	steerable bool
	pending   []ports.Message
}

func NewActiveTurns() *ActiveTurns {
	return &ActiveTurns{active: map[string]*activeTurn{}}
}

// Begin derives a cancellable context for the turn ctx belongs to and
// registers it. steerable declares whether a message arriving mid-turn may
// join it. The returned release function must be called when the turn ends;
// it cancels and deregisters, and is safe to call more than once.
func (t *ActiveTurns) Begin(ctx context.Context, steerable bool) (context.Context, func()) {
	conversation := destination.FromContext(ctx).ConversationID()
	turnContext, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.next++
	turn := &activeTurn{id: t.next, cancel: cancel, steerable: steerable}
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

// Steer hands text to the turn already running in ctx's conversation,
// reporting whether one accepted it. A conversation with no running turn, or
// one running a turn that is not steerable, reports false and the caller
// starts an ordinary turn instead.
func (t *ActiveTurns) Steer(ctx context.Context, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	conversation := destination.FromContext(ctx).ConversationID()
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.active[conversation]
	if !ok || !turn.steerable {
		return false
	}
	turn.pending = append(turn.pending, ports.Message{Role: ports.RoleUser, Content: text})
	return true
}

// Pending drains whatever has been steered into ctx's conversation since the
// last call. It is what a running loop calls at each step boundary, so it
// must stay non-blocking and must never return the same message twice.
func (t *ActiveTurns) Pending(ctx context.Context) []ports.Message {
	conversation := destination.FromContext(ctx).ConversationID()
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.active[conversation]
	if !ok || len(turn.pending) == 0 {
		return nil
	}
	pending := turn.pending
	turn.pending = nil
	return pending
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
