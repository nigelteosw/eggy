package jsonfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/atomicfile"
	"github.com/nigelteosw/eggy/internal/adapters/filelock"
	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

const CurrentSchemaVersion = 4

var ErrVersionConflict = ports.ErrStateVersionConflict

type Store struct {
	path string
	mu   sync.Mutex
}

func Open(path string) *Store { return &Store{path: path} }

func initialState() ports.State {
	return ports.State{
		SchemaVersion:   CurrentSchemaVersion,
		Approvals:       map[string]approvals.Approval{},
		Schedules:       map[string]ports.Schedule{},
		ProcessedEvents: map[string]time.Time{},
	}
}

func (s *Store) Load(ctx context.Context) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var state ports.State
	var loadErr error
	err := filelock.With(s.path, func() error { state, loadErr = s.loadUnlocked(); return loadErr })
	return state, err
}

func (s *Store) loadUnlocked() (ports.State, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return initialState(), nil
	}
	if err != nil {
		return ports.State{}, fmt.Errorf("read state: %w", err)
	}
	state := initialState()
	if err := json.Unmarshal(data, &state); err != nil {
		return ports.State{}, fmt.Errorf("decode state: %w", err)
	}
	if state.SchemaVersion < CurrentSchemaVersion {
		state.SchemaVersion = CurrentSchemaVersion
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return ports.State{}, fmt.Errorf("encode migrated state: %w", err)
		}
		if err := atomicfile.Write(s.path, append(data, '\n'), 0o600); err != nil {
			return ports.State{}, fmt.Errorf("persist migrated state: %w", err)
		}
	} else if state.SchemaVersion != CurrentSchemaVersion {
		return ports.State{}, fmt.Errorf("unsupported state schema %d", state.SchemaVersion)
	}
	return state, nil
}

func (s *Store) Update(ctx context.Context, expectedVersion uint64, mutate func(*ports.State) error) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var updated ports.State
	err := filelock.With(s.path, func() error {
		state, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if state.Version != expectedVersion {
			return fmt.Errorf("%w: expected %d, current %d", ports.ErrStateVersionConflict, expectedVersion, state.Version)
		}
		copy, err := clone(state)
		if err != nil {
			return err
		}
		if err := mutate(&copy); err != nil {
			return err
		}
		copy.Version++
		copy.SchemaVersion = CurrentSchemaVersion
		data, err := json.MarshalIndent(copy, "", "  ")
		if err != nil {
			return fmt.Errorf("encode state: %w", err)
		}
		data = append(data, '\n')
		if err := atomicfile.Write(s.path, data, 0o600); err != nil {
			return err
		}
		updated = copy
		return nil
	})
	return updated, err
}

func clone(state ports.State) (ports.State, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return ports.State{}, fmt.Errorf("clone state: %w", err)
	}
	copy := initialState()
	if err := json.Unmarshal(data, &copy); err != nil {
		return ports.State{}, fmt.Errorf("clone state: %w", err)
	}
	return copy, nil
}
