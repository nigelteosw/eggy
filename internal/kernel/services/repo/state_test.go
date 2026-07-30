package repo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type memoryStore struct {
	mu    sync.Mutex
	state ports.State
}

func newMemoryStore() *memoryStore {
	return &memoryStore{state: ports.State{
		SchemaVersion:   1,
		ProcessedEvents: map[string]time.Time{},
		Approvals:       map[string]approvals.Approval{},
	}}
}

func (s *memoryStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *memoryStore) Update(_ context.Context, expected uint64, mutate func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Version != expected {
		return ports.State{}, errors.New("conflict")
	}
	if err := mutate(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}
