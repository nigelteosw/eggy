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
// Cancellation only stops the loop from taking further steps. A thread's
// read-only checkout survives, so later inspection can continue.
//
// Steering is the other half: a message that arrives while a turn is running
// joins that turn at its next step boundary instead of starting a competing
// one. Only turns marked steerable accept it -- a scheduled turn
// is deliberately self-contained, and an owner message must never be folded
// into one.
//
// Turns are keyed by account and conversation together. Telegram's fixed
// conversation ID is the same string for every account, so without the
// account in the key one person's /stop would cancel another's turn and a
// steered message could land in a stranger's loop.
type ActiveTurns struct {
	mu     sync.Mutex
	next   uint64
	active map[turnKey]*activeTurn
}

type turnKey struct{ account, conversation string }

// keyOf is the one place a turn's identity is derived from its context. A
// missing principal yields an empty account, which matches nothing that
// Begin registered: an unauthenticated caller can neither stop nor steer.
func keyOf(ctx context.Context) turnKey {
	principal, _ := ports.PrincipalFromContext(ctx)
	return turnKey{account: principal.AccountID, conversation: destination.FromContext(ctx).ConversationID()}
}

type activeTurn struct {
	id        uint64
	cancel    context.CancelFunc
	steerable bool
	pending   []ports.Message
}

func NewActiveTurns() *ActiveTurns {
	return &ActiveTurns{active: map[turnKey]*activeTurn{}}
}

// Begin derives a cancellable context for the turn ctx belongs to and
// registers it. steerable declares whether a message arriving mid-turn may
// join it. The returned release function must be called when the turn ends;
// it cancels and deregisters, and is safe to call more than once.
//
// Release returns whatever was steered but never drained -- a message that
// arrived after the turn's last step boundary, which the turn accepted and
// then had no step left to read it in. The caller is what makes that message
// a turn of its own; dropping it here is the one outcome steering must never
// have, because the owner was told nothing and the turn did nothing.
func (t *ActiveTurns) Begin(ctx context.Context, steerable bool) (context.Context, func() []ports.Message) {
	conversation := keyOf(ctx)
	turnContext, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.next++
	turn := &activeTurn{id: t.next, cancel: cancel, steerable: steerable}
	// A second turn in the same conversation replaces the first as the
	// cancellation target; the first still cancels through its own release.
	t.active[conversation] = turn
	t.mu.Unlock()
	return turnContext, func() []ports.Message {
		t.mu.Lock()
		// Only deregister if this turn is still the registered one, so a
		// finishing turn never clears a newer turn's cancellation.
		if current, ok := t.active[conversation]; ok && current.id == turn.id {
			delete(t.active, conversation)
		}
		// Taken under the same lock that deregisters, so no steer can land
		// between the two and be lost: after this, Steer can no longer find
		// this turn, and anything it did find is in undrained.
		undrained := turn.pending
		turn.pending = nil
		t.mu.Unlock()
		cancel()
		return undrained
	}
}

// Steer hands text to the turn already running in ctx's conversation,
// reporting whether one accepted it. A conversation with no running turn, or
// one running a turn that is not steerable, reports false and the caller
// starts an ordinary turn instead.
func (t *ActiveTurns) Steer(ctx context.Context, message ports.Message) bool {
	if strings.TrimSpace(message.Content) == "" && len(message.Parts) == 0 {
		return false
	}
	conversation := keyOf(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	turn, ok := t.active[conversation]
	if !ok || !turn.steerable {
		return false
	}
	message.Role = ports.RoleUser
	turn.pending = append(turn.pending, message)
	return true
}

// Pending drains whatever has been steered into ctx's conversation since the
// last call. It is what a running loop calls at each step boundary, so it
// must stay non-blocking and must never return the same message twice.
func (t *ActiveTurns) Pending(ctx context.Context) []ports.Message {
	conversation := keyOf(ctx)
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
	conversation := keyOf(ctx)
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

// Active reports whether any conversation currently has a turn running.
func (t *ActiveTurns) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active) > 0
}
