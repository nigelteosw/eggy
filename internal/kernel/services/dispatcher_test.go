package services

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/kernel/tasks"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestDispatcherEnforcesOwnerAndDeduplicates(t *testing.T) {
	store := newMemoryStore()
	calls := 0
	dispatcher := NewDispatcher("42", store, map[events.Type]EventHandler{
		events.TypeMessage: func(context.Context, events.Event) error { calls++; return nil },
	})
	event := events.Event{ID: "telegram:1", Type: events.TypeMessage, Owner: "42", Timestamp: time.Now(), Payload: json.RawMessage(`{"text":"hi"}`)}
	if err := dispatcher.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("handler calls=%d", calls)
	}
	event.ID = "telegram:2"
	event.Owner = "99"
	if err := dispatcher.Handle(context.Background(), event); !errors.Is(err, ErrOwnerDenied) {
		t.Fatalf("expected owner denial, got %v", err)
	}
}

func TestDispatcherDoesNotDeduplicateFailedHandler(t *testing.T) {
	store := newMemoryStore()
	calls := 0
	dispatcher := NewDispatcher("42", store, map[events.Type]EventHandler{
		events.TypeMessage: func(context.Context, events.Event) error {
			calls++
			if calls == 1 {
				return errors.New("transient")
			}
			return nil
		},
	})
	event := events.Event{ID: "event-1", Type: events.TypeMessage, Owner: "42"}
	if err := dispatcher.Handle(context.Background(), event); err == nil {
		t.Fatal("expected handler error")
	}
	if err := dispatcher.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestDispatcherMarksEventAfterHandlerMutatesSharedState(t *testing.T) {
	store := newMemoryStore()
	dispatcher := NewDispatcher("42", store, map[events.Type]EventHandler{
		events.TypeMessage: func(ctx context.Context, _ events.Event) error {
			state, _ := store.Load(ctx)
			_, err := store.Update(ctx, state.Version, func(state *ports.State) error {
				state.SelectedRepository = "eggy"
				return nil
			})
			return err
		},
	})
	if err := dispatcher.Handle(context.Background(), events.Event{ID: "event-with-write", Type: events.TypeMessage, Owner: "42"}); err != nil {
		t.Fatalf("Handle() after handler state write: %v", err)
	}
	state, _ := store.Load(context.Background())
	if _, ok := state.ProcessedEvents["event-with-write"]; !ok {
		t.Fatal("event was not marked processed")
	}
}

func TestDispatcherSerializesConcurrentDuplicateEvents(t *testing.T) {
	store := newMemoryStore()
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	dispatcher := NewDispatcher("42", store, map[events.Type]EventHandler{
		events.TypeMessage: func(context.Context, events.Event) error {
			calls++
			if calls == 1 {
				close(started)
				<-release
			}
			return nil
		},
	})
	event := events.Event{ID: "same", Type: events.TypeMessage, Owner: "42"}
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- dispatcher.Handle(context.Background(), event) }()
	<-started
	go func() { errorsChannel <- dispatcher.Handle(context.Background(), event) }()
	close(release)
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("duplicate handler calls=%d", calls)
	}
}

func TestDispatcherAllowsDifferentEventsConcurrently(t *testing.T) {
	store := newMemoryStore()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondDone := make(chan error, 1)
	dispatcher := NewDispatcher("42", store, map[events.Type]EventHandler{
		events.TypeMessage: func(_ context.Context, event events.Event) error {
			if event.ID == "first" {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		},
	})
	go func() {
		_ = dispatcher.Handle(context.Background(), events.Event{ID: "first", Type: events.TypeMessage, Owner: "42"})
	}()
	<-firstStarted
	go func() {
		secondDone <- dispatcher.Handle(context.Background(), events.Event{ID: "second", Type: events.TypeMessage, Owner: "42"})
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		close(releaseFirst)
		t.Fatal("different event was blocked behind a long-running handler")
	}
	close(releaseFirst)
}

type memoryStore struct {
	mu    sync.Mutex
	state ports.State
}

func newMemoryStore() *memoryStore {
	return &memoryStore{state: ports.State{SchemaVersion: 1, ProcessedEvents: map[string]time.Time{}, Tasks: map[string]tasks.Task{}, Approvals: map[string]approvals.Approval{}}}
}

func (s *memoryStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}
func (s *memoryStore) Update(_ context.Context, expected uint64, fn func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Version != expected {
		return ports.State{}, errors.New("conflict")
	}
	if err := fn(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}
