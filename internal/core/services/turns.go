package services

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ActiveTurns serializes admission for each account and conversation. The
// admission section is short: it prepares one input, then either publishes it
// to the compatible execution owner or establishes a new owner. Long model
// execution happens after admission has been released.
type ActiveTurns struct {
	mu      sync.Mutex
	entries map[turnKey]*turnEntry
	active  atomic.Int64
}

type turnKey struct{ account, conversation string }

func keyOf(ctx context.Context) (turnKey, error) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return turnKey{}, err
	}
	return turnKey{account: principal.AccountID, conversation: destination.FromContext(ctx).ConversationID()}, nil
}

type turnEntry struct {
	mu         sync.Mutex
	next       uint64
	active     *activeTurn
	preparing  bool
	prepared   chan struct{}
	waiters    []*admissionWaiter
	references int
}

type admissionWaiter struct {
	ready     chan struct{}
	steerable bool
	joinable  bool
	granted   bool
}

type activeTurn struct {
	generation uint64
	cancel     context.CancelFunc
	steerable  bool
	stopping   bool
	pending    []ports.Message
}

type generationBinding struct {
	key        turnKey
	entry      *turnEntry
	generation uint64
}

type generationKey struct{}

// TurnAdmission reports whether the caller owns model execution. Context is
// generation-bound for an owner and must be used for Pending and Release.
// Joined callers have published their input to the existing owner and return
// without executing the loop.
type TurnAdmission struct {
	Context context.Context
	Owner   bool
}

func NewActiveTurns() *ActiveTurns {
	return &ActiveTurns{entries: map[turnKey]*turnEntry{}}
}

func (t *ActiveTurns) acquireEntry(key turnKey) *turnEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[key]
	if entry == nil {
		entry = &turnEntry{}
		t.entries[key] = entry
	}
	entry.references++
	return entry
}

func (t *ActiveTurns) findEntry(key turnKey) *turnEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[key]
	if entry != nil {
		entry.references++
	}
	return entry
}

func (t *ActiveTurns) releaseEntry(key turnKey, entry *turnEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry.mu.Lock()
	entry.references--
	idle := entry.references == 0 && entry.active == nil && !entry.preparing && len(entry.waiters) == 0
	entry.mu.Unlock()
	if idle && t.entries[key] == entry {
		delete(t.entries, key)
	}
}

func (entry *turnEntry) grantNextLocked() {
	if entry.preparing || len(entry.waiters) == 0 {
		return
	}
	waiter := entry.waiters[0]
	if entry.active != nil && (!waiter.joinable || !waiter.steerable || !entry.active.steerable || entry.active.stopping) {
		return
	}
	entry.waiters = entry.waiters[1:]
	entry.preparing = true
	entry.prepared = make(chan struct{})
	waiter.granted = true
	close(waiter.ready)
}

func (entry *turnEntry) finishPreparationLocked() {
	entry.preparing = false
	close(entry.prepared)
	entry.prepared = nil
	entry.grantNextLocked()
}

// Admit atomically prepares and publishes one input for its account and
// conversation. prepare runs while admission for only that key is exclusive;
// owner says whether this input will establish a new execution owner.
func (t *ActiveTurns) Admit(ctx context.Context, message ports.Message, steerable bool, prepare func(owner bool) error) (TurnAdmission, error) {
	key, err := keyOf(ctx)
	if err != nil {
		return TurnAdmission{}, err
	}
	entry := t.acquireEntry(key)
	defer t.releaseEntry(key, entry)
	waiter := &admissionWaiter{
		ready:     make(chan struct{}),
		steerable: steerable,
		joinable:  strings.TrimSpace(message.Content) != "" || len(message.Parts) != 0,
	}
	entry.mu.Lock()
	entry.waiters = append(entry.waiters, waiter)
	entry.grantNextLocked()
	entry.mu.Unlock()

	select {
	case <-waiter.ready:
	case <-ctx.Done():
		entry.mu.Lock()
		if waiter.granted {
			entry.finishPreparationLocked()
		} else {
			for index, queued := range entry.waiters {
				if queued == waiter {
					entry.waiters = append(entry.waiters[:index], entry.waiters[index+1:]...)
					break
				}
			}
			entry.grantNextLocked()
		}
		entry.mu.Unlock()
		return TurnAdmission{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		entry.mu.Lock()
		entry.finishPreparationLocked()
		entry.mu.Unlock()
		return TurnAdmission{}, err
	}

	entry.mu.Lock()
	owner := entry.active == nil
	entry.mu.Unlock()
	if prepare != nil {
		if err := prepare(owner); err != nil {
			entry.mu.Lock()
			entry.finishPreparationLocked()
			entry.mu.Unlock()
			return TurnAdmission{}, err
		}
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if owner {
		entry.next++
		execution, cancel := context.WithCancel(ctx)
		binding := generationBinding{key: key, entry: entry, generation: entry.next}
		execution = context.WithValue(execution, generationKey{}, binding)
		entry.active = &activeTurn{generation: entry.next, cancel: cancel, steerable: steerable}
		t.active.Add(1)
		entry.finishPreparationLocked()
		return TurnAdmission{Context: execution, Owner: true}, nil
	}
	message.Role = ports.RoleUser
	entry.active.pending = append(entry.active.pending, message)
	entry.finishPreparationLocked()
	return TurnAdmission{Context: ctx}, nil
}

// Pending drains input only for the execution generation carried by ctx.
func (t *ActiveTurns) Pending(ctx context.Context) []ports.Message {
	binding, ok := ctx.Value(generationKey{}).(generationBinding)
	if !ok {
		return nil
	}
	entry := binding.entry
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.active == nil || entry.active.generation != binding.generation || len(entry.active.pending) == 0 {
		return nil
	}
	pending := entry.active.pending
	entry.active.pending = nil
	return pending
}

// Release closes only the execution generation carried by ctx and returns
// input that owner accepted but never drained. It is safe to call repeatedly.
func (t *ActiveTurns) Release(ctx context.Context) []ports.Message {
	binding, ok := ctx.Value(generationKey{}).(generationBinding)
	if !ok {
		return nil
	}
	entry := binding.entry
	for {
		entry.mu.Lock()
		if entry.preparing {
			prepared := entry.prepared
			entry.mu.Unlock()
			<-prepared
			continue
		}
		turn := entry.active
		if turn == nil || turn.generation != binding.generation {
			entry.mu.Unlock()
			return nil
		}
		entry.active = nil
		pending := turn.pending
		turn.pending = nil
		turn.cancel()
		t.active.Add(-1)
		entry.grantNextLocked()
		entry.mu.Unlock()
		t.cleanup(binding.key, entry)
		return pending
	}
}

func (t *ActiveTurns) cleanup(key turnKey, entry *turnEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if t.entries[key] == entry && entry.references == 0 && entry.active == nil && !entry.preparing && len(entry.waiters) == 0 {
		delete(t.entries, key)
	}
}

// Stop marks and cancels the current owner. Its registration remains until
// the worker releases it, preventing a replacement execution from overlap.
func (t *ActiveTurns) Stop(ctx context.Context) bool {
	key, err := keyOf(ctx)
	if err != nil {
		return false
	}
	entry := t.findEntry(key)
	if entry == nil {
		return false
	}
	defer t.releaseEntry(key, entry)
	for {
		entry.mu.Lock()
		if entry.preparing {
			prepared := entry.prepared
			entry.mu.Unlock()
			<-prepared
			continue
		}
		if entry.active == nil || entry.active.stopping {
			entry.mu.Unlock()
			return false
		}
		entry.active.stopping = true
		entry.active.cancel()
		entry.mu.Unlock()
		return true
	}
}

func (t *ActiveTurns) Active() bool {
	return t.active.Load() != 0
}
