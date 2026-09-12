package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

var ErrOwnerDenied = errors.New("event owner denied")

// processedEventRetention bounds the deduplication ledger. Every handled event
// writes one entry, so without a cutoff `state.json` grows for the life of the
// deployment and every dispatch pays a full Load plus Update of the growing
// map. The window only has to outlast redelivery: Telegram retries an
// unconfirmed update for minutes, and a restart replays at most the queue it
// was holding. A week is far past both and keeps the ledger proportional to a
// week of traffic rather than to uptime.
const processedEventRetention = 7 * 24 * time.Hour

type EventHandler func(context.Context, events.Event) error

// AccountResolver reports whether an account ID names a configured account.
// It is a closure over validated config, wired by bootstrap, so the dispatcher
// never sees config and a removed account stops resolving on the next event.
type AccountResolver func(accountID string) bool

type Dispatcher struct {
	locksMu  sync.Mutex
	locks    map[string]*eventLock
	accounts AccountResolver
	store    ports.StateStore
	handlers map[events.Type]EventHandler
}

func NewDispatcher(accounts AccountResolver, store ports.StateStore, handlers map[events.Type]EventHandler) *Dispatcher {
	return &Dispatcher{accounts: accounts, store: store, handlers: handlers, locks: map[string]*eventLock{}}
}

// Handle runs one event as the account that owns it. Event.Owner was set by
// a trusted ingress -- a verified session, a verified Telegram sender, the
// stored owner of a schedule -- and is checked against the configured
// accounts here, once, before the principal is put on the context that every
// private read and write downstream requires. Nothing after this point
// decides who is acting.
func (d *Dispatcher) Handle(ctx context.Context, event events.Event) error {
	release := d.lockEvent(event.ID)
	defer release()
	if event.Owner == "" || !d.accounts(event.Owner) {
		return ErrOwnerDenied
	}
	ctx = ports.WithPrincipal(ctx, ports.Principal{AccountID: event.Owner})
	state, err := d.store.Load(ctx)
	if err != nil {
		return err
	}
	if _, seen := state.ProcessedEvents[event.ID]; seen {
		return nil
	}
	handler, ok := d.handlers[event.Type]
	if !ok {
		return fmt.Errorf("no handler for event type %q", event.Type)
	}
	if err := handler(ctx, event); err != nil {
		return err
	}
	state, err = d.store.Load(ctx)
	if err != nil {
		return err
	}
	if _, seen := state.ProcessedEvents[event.ID]; seen {
		return nil
	}
	// The recorded time is when the event was handled, not the event's own
	// timestamp: nothing reads the value except this retention sweep, and a
	// sweep must measure how long ago Eggy saw an event. Keying off the
	// producer's clock would expire a replayed old event on the write that
	// recorded it, silently dropping the deduplication it exists for.
	now := time.Now().UTC()
	_, err = d.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.ProcessedEvents == nil {
			state.ProcessedEvents = map[string]time.Time{}
		}
		cutoff := now.Add(-processedEventRetention)
		for id, handled := range state.ProcessedEvents {
			if handled.Before(cutoff) {
				delete(state.ProcessedEvents, id)
			}
		}
		state.ProcessedEvents[event.ID] = now
		return nil
	})
	return err
}

type eventLock struct {
	mu         sync.Mutex
	references int
}

func (d *Dispatcher) lockEvent(id string) func() {
	d.locksMu.Lock()
	lock := d.locks[id]
	if lock == nil {
		lock = &eventLock{}
		d.locks[id] = lock
	}
	lock.references++
	d.locksMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		d.locksMu.Lock()
		lock.references--
		if lock.references == 0 {
			delete(d.locks, id)
		}
		d.locksMu.Unlock()
	}
}
