package repo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// memoryChangeStore is the Change-side fake. Two fakes rather than one is
// the point of the split: neither concept has to carry the other's fields.
type memoryChangeStore struct {
	changes map[string]ports.Change
}

func newMemoryChangeStore() *memoryChangeStore {
	return &memoryChangeStore{changes: map[string]ports.Change{}}
}

func (s *memoryChangeStore) Create(_ context.Context, change ports.Change) (ports.Change, error) {
	s.changes[change.ID] = change
	return change, nil
}

func (s *memoryChangeStore) Load(_ context.Context, id string) (ports.Change, error) {
	return s.changes[id], nil
}

func (s *memoryChangeStore) List(context.Context) ([]ports.Change, error) {
	result := make([]ports.Change, 0, len(s.changes))
	for _, change := range s.changes {
		result = append(result, change)
	}
	return result, nil
}

func (s *memoryChangeStore) Update(_ context.Context, id string, mutate func(*ports.Change) error) (ports.Change, error) {
	change := s.changes[id]
	if err := mutate(&change); err != nil {
		return ports.Change{}, err
	}
	s.changes[id] = change
	return change, nil
}

// memoryStore is a ports.StateStore fake. It is duplicated from the services
// package's own test fake rather than shared: a test fake is not API, and a
// kerneltest package exported purely so two test packages can agree on a
// forty-line stub would be a worse trade than the copy.
type memoryStore struct {
	mu    sync.Mutex
	state ports.State
}

func newMemoryStore() *memoryStore {
	return &memoryStore{state: ports.State{SchemaVersion: 1, ProcessedEvents: map[string]time.Time{}, Approvals: map[string]approvals.Approval{}}}
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

// memoryTranscriptStore is a ports.TranscriptStore fake, duplicated for the
// same reason as memoryStore above.
type memoryTranscriptStore struct {
	transcripts map[string]ports.Transcript
}

func newMemoryTranscriptStore() *memoryTranscriptStore {
	return &memoryTranscriptStore{transcripts: map[string]ports.Transcript{}}
}

func (s *memoryTranscriptStore) Create(_ context.Context, transcript ports.Transcript) (ports.Transcript, error) {
	s.transcripts[transcript.ID] = transcript
	return transcript, nil
}

func (s *memoryTranscriptStore) Load(_ context.Context, id string) (ports.Transcript, error) {
	return s.transcripts[id], nil
}

func (s *memoryTranscriptStore) List(context.Context) ([]ports.Transcript, error) {
	result := make([]ports.Transcript, 0, len(s.transcripts))
	for _, transcript := range s.transcripts {
		result = append(result, transcript)
	}
	return result, nil
}

func (s *memoryTranscriptStore) AppendEvent(_ context.Context, id string, event ports.TranscriptEvent) (ports.Transcript, error) {
	transcript := s.transcripts[id]
	transcript.Events = append(transcript.Events, event)
	s.transcripts[id] = transcript
	return transcript, nil
}

func (s *memoryTranscriptStore) Update(_ context.Context, id string, mutate func(*ports.Transcript) error) (ports.Transcript, error) {
	transcript := s.transcripts[id]
	if err := mutate(&transcript); err != nil {
		return ports.Transcript{}, err
	}
	s.transcripts[id] = transcript
	return transcript, nil
}

type fakeWorkspaceRunner struct {
	workspace          string
	created, destroyed bool
}

func (r *fakeWorkspaceRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *fakeWorkspaceRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *fakeWorkspaceRunner) Destroy(context.Context, string) error { r.destroyed = true; return nil }
