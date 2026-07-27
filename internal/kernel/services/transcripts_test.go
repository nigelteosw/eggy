package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestTranscriptsOpenRecordsTheTurnsInstruction(t *testing.T) {
	store := newMemoryTranscriptStore()
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	transcripts := NewTranscripts(store, 80, func() time.Time { return now })

	transcript, err := transcripts.Open(context.Background(), "turn-1", "Add resumable sessions\nwith a durable workspace")
	if err != nil {
		t.Fatal(err)
	}
	if !transcript.StartedAt.Equal(now) || transcript.Instruction == "" {
		t.Fatalf("transcript=%#v", transcript)
	}
}

// The transcript bounds and redacts each event it stores. It keeps no
// compacted context of its own any more -- what a turn can still see is
// agent.ContextPolicy's business.
func TestImplementationSessionsBoundsAndRedactsTranscriptEvents(t *testing.T) {
	store := newMemoryTranscriptStore()
	transcripts := NewTranscripts(store, 12, time.Now, "live-secret")
	if _, err := transcripts.Open(context.Background(), "turn-1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := transcripts.Append(context.Background(), "turn-1", ports.TranscriptEvent{
		Kind: ports.SessionToolResult, ToolName: "terminal", Message: "Validation: command failed", Content: "live-secret output that exceeds the retained budget", ModelMessage: ports.Message{Role: ports.RoleTool, Content: "live-secret output that exceeds the retained budget"},
	}); err != nil {
		t.Fatal(err)
	}

	session, err := transcripts.Load(context.Background(), "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Events) != 1 {
		t.Fatalf("events=%#v, want the appended event", session.Events)
	}
	event := session.Events[0]
	if strings.Contains(event.Content, "live-secret") || strings.Contains(event.ModelMessage.Content, "live-secret") {
		t.Fatalf("secret retained in transcript event=%#v", event)
	}
	if len(event.Content) > 12 || len(event.ModelMessage.Content) > 12 {
		t.Fatalf("event exceeds the configured excerpt bound: %#v", event)
	}
}

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
