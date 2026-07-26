package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// CodingService is what remains once "a coding run" stopped being a mode:
// bookkeeping over the sessions that workspace_edit opens and propose_change
// ships. It drives no loop and owns no checkout -- the checkout belongs to
// the conversation thread (see WorkspaceSessions), and the editing is
// ordinary turns of the one loop.
type CodingService struct {
	sessions *ImplementationSessions
}

func NewCodingService(sessions *ImplementationSessions) *CodingService {
	return &CodingService{sessions: sessions}
}

// List returns every session's canonical record, for status and /runs
// reporting.
func (s *CodingService) List(ctx context.Context) ([]ports.ImplementationSession, error) {
	if s.sessions == nil {
		return nil, nil
	}
	return s.sessions.List(ctx)
}

func (s *CodingService) RecoverInterrupted(ctx context.Context) (int, error) {
	if s.sessions == nil {
		return 0, nil
	}
	return s.sessions.MarkInterrupted(ctx)
}

// Cleanup releases a finished session's claim on its checkout. It does not
// destroy the directory: the checkout belongs to the conversation thread,
// which keeps reading it after the change ships. Reaping it is
// WorkspaceSessions.CleanupIdle's job, once the thread itself goes quiet.
func (s *CodingService) Cleanup(ctx context.Context, runID string) error {
	if s.sessions == nil {
		return errors.New("implementation sessions are unavailable")
	}
	if _, err := s.sessions.Load(ctx, runID); err != nil {
		return fmt.Errorf("coding session %q not found: %w", runID, err)
	}
	return s.sessions.ClearWorkspace(ctx, runID)
}

func (s *CodingService) CleanupExpired(ctx context.Context, cutoff time.Time) error {
	if s.sessions == nil {
		return nil
	}
	sessions, err := s.sessions.List(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Workspace != "" && !session.FinishedAt.IsZero() && session.FinishedAt.Before(cutoff) {
			if err := s.Cleanup(ctx, session.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
