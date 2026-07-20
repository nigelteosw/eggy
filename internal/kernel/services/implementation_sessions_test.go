package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestImplementationSessionsCreatesOwnerTriggeredSession(t *testing.T) {
	store := newMemorySessionStore()
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	sessions := NewImplementationSessions(store, SessionPolicy{ContextBudgetChars: 200, RecentMessages: 2, OutputExcerptChars: 80}, func() time.Time { return now })

	session, err := sessions.Create(context.Background(), ports.ImplementationSession{
		ID:          "run-1",
		Repository:  "eggy",
		Instruction: "Add resumable sessions\nwith a durable workspace",
		Workspace:   "/data/runs/run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != ports.SessionCreated || session.Title != "Add resumable sessions" || !session.StartedAt.Equal(now) {
		t.Fatalf("session=%#v", session)
	}
}

func TestImplementationSessionsCompactsAndRedactsTranscript(t *testing.T) {
	store := newMemorySessionStore()
	sessions := NewImplementationSessions(store, SessionPolicy{ContextBudgetChars: 40, RecentMessages: 1, OutputExcerptChars: 12}, time.Now, "live-secret")
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Instruction: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Append(context.Background(), "run-1", ports.ImplementationSessionEvent{
		Kind: ports.SessionToolResult, ToolName: "terminal", Message: "Validation: command failed", Content: "live-secret output that exceeds the retained budget", ModelMessage: ports.Message{Role: ports.RoleTool, Content: "live-secret output that exceeds the retained budget"},
	}); err != nil {
		t.Fatal(err)
	}

	session, err := sessions.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(session.Context.Summary, "live-secret") || strings.Contains(session.Context.RecentMessages[0].Content, "live-secret") {
		t.Fatalf("secret retained in context=%#v", session.Context)
	}
	if len(session.Context.RecentMessages) != 1 || len(session.Context.RecentMessages[0].Content) > 12 {
		t.Fatalf("recent context=%#v", session.Context)
	}
}

type memorySessionStore struct {
	sessions map[string]ports.ImplementationSession
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{sessions: map[string]ports.ImplementationSession{}}
}

func (s *memorySessionStore) Create(_ context.Context, session ports.ImplementationSession) (ports.ImplementationSession, error) {
	s.sessions[session.ID] = session
	return session, nil
}

func (s *memorySessionStore) Load(_ context.Context, id string) (ports.ImplementationSession, error) {
	return s.sessions[id], nil
}

func (s *memorySessionStore) List(context.Context) ([]ports.ImplementationSession, error) {
	result := make([]ports.ImplementationSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	return result, nil
}

func (s *memorySessionStore) AppendEvent(_ context.Context, id string, event ports.ImplementationSessionEvent) (ports.ImplementationSession, error) {
	session := s.sessions[id]
	session.Events = append(session.Events, event)
	s.sessions[id] = session
	return session, nil
}

func (s *memorySessionStore) Update(_ context.Context, id string, mutate func(*ports.ImplementationSession) error) (ports.ImplementationSession, error) {
	session := s.sessions[id]
	if err := mutate(&session); err != nil {
		return ports.ImplementationSession{}, err
	}
	s.sessions[id] = session
	return session, nil
}
