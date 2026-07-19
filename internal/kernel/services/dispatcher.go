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

type EventHandler func(context.Context, events.Event) error

type Dispatcher struct {
	locksMu  sync.Mutex
	locks    map[string]*eventLock
	owner    string
	store    ports.StateStore
	handlers map[events.Type]EventHandler
}

func NewDispatcher(owner string, store ports.StateStore, handlers map[events.Type]EventHandler) *Dispatcher {
	return &Dispatcher{owner: owner, store: store, handlers: handlers, locks: map[string]*eventLock{}}
}

func (d *Dispatcher) Handle(ctx context.Context, event events.Event) error {
	release := d.lockEvent(event.ID)
	defer release()
	if event.Owner != d.owner {
		return ErrOwnerDenied
	}
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
	when := event.Timestamp
	if when.IsZero() {
		when = time.Now().UTC()
	}
	_, err = d.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.ProcessedEvents == nil {
			state.ProcessedEvents = map[string]time.Time{}
		}
		state.ProcessedEvents[event.ID] = when
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
