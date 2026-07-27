package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

type SessionPolicy struct {
	ContextBudgetChars int
	RecentMessages     int
	OutputExcerptChars int
}

func (p SessionPolicy) normalized() SessionPolicy {
	if p.ContextBudgetChars <= 0 {
		p.ContextBudgetChars = 96000
	}
	if p.RecentMessages <= 0 {
		p.RecentMessages = 16
	}
	if p.OutputExcerptChars <= 0 {
		p.OutputExcerptChars = 8192
	}
	return p
}

type ImplementationSessions struct {
	store  ports.ImplementationSessionStore
	policy SessionPolicy
	now    func() time.Time
	guard  *SecretGuard
}

func NewImplementationSessions(store ports.ImplementationSessionStore, policy SessionPolicy, now func() time.Time, activeSecrets ...string) *ImplementationSessions {
	if now == nil {
		now = time.Now
	}
	return &ImplementationSessions{store: store, policy: policy.normalized(), now: now, guard: NewSecretGuard(activeSecrets)}
}

func (s *ImplementationSessions) Create(ctx context.Context, session ports.ImplementationSession) (ports.ImplementationSession, error) {
	if s.store == nil {
		return ports.ImplementationSession{}, errors.New("implementation session store is unavailable")
	}
	if strings.TrimSpace(session.ID) == "" {
		return ports.ImplementationSession{}, errors.New("implementation session id is required")
	}
	now := s.now()
	if session.Phase == "" {
		session.Phase = ports.PhaseRunning
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = now
	}
	session.UpdatedAt = now
	return s.store.Create(ctx, s.sanitizeSession(session))
}

func (s *ImplementationSessions) Load(ctx context.Context, id string) (ports.ImplementationSession, error) {
	if s.store == nil {
		return ports.ImplementationSession{}, errors.New("implementation session store is unavailable")
	}
	return s.store.Load(ctx, id)
}

// List returns every persisted session, most-recently-updated first.
func (s *ImplementationSessions) List(ctx context.Context) ([]ports.ImplementationSession, error) {
	if s.store == nil {
		return nil, errors.New("implementation session store is unavailable")
	}
	return s.store.List(ctx)
}

// Runs returns the sessions that actually branched a repository, most
// recently updated first, for status and /runs reporting. Every turn now
// writes a durable transcript into the same store, so a conversation about
// the weather is a transcript, not a run, and listing it as one would drown
// the view that matters.
func (s *ImplementationSessions) Runs(ctx context.Context) ([]ports.ImplementationSession, error) {
	sessions, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	runs := make([]ports.ImplementationSession, 0, len(sessions))
	for _, session := range sessions {
		if IsCodingSession(session) {
			runs = append(runs, session)
		}
	}
	return runs, nil
}

// IsCodingSession distinguishes a session that branched a repository from a
// plain turn transcript. A repository and a checkout are what workspace_edit
// records; a transcript has neither, only the record of what was said and
// called.
func IsCodingSession(session ports.ImplementationSession) bool {
	return session.Repository != "" || session.Workspace != ""
}

// ReleaseWorkspace releases a finished session's claim on its checkout. It
// does not destroy the directory: the checkout belongs to the conversation
// thread, which keeps reading it after the change ships. Reaping it is
// WorkspaceSessions.CleanupIdle's job, once the thread itself goes quiet.
func (s *ImplementationSessions) ReleaseWorkspace(ctx context.Context, id string) error {
	if _, err := s.Load(ctx, id); err != nil {
		return fmt.Errorf("coding session %q not found: %w", id, err)
	}
	return s.ClearWorkspace(ctx, id)
}

// ReleaseExpiredWorkspaces releases every session that stopped progressing
// before cutoff.
func (s *ImplementationSessions) ReleaseExpiredWorkspaces(ctx context.Context, cutoff time.Time) error {
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Workspace != "" && !session.FinishedAt.IsZero() && session.FinishedAt.Before(cutoff) {
			if err := s.ReleaseWorkspace(ctx, session.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ImplementationSessions) Append(ctx context.Context, id string, event ports.ImplementationSessionEvent) (ports.ImplementationSession, error) {
	if s.store == nil {
		return ports.ImplementationSession{}, errors.New("implementation session store is unavailable")
	}
	event = s.sanitizeEvent(event)
	if event.At.IsZero() {
		event.At = s.now()
	}
	if _, err := s.store.AppendEvent(ctx, id, event); err != nil {
		return ports.ImplementationSession{}, err
	}
	return s.store.Update(ctx, id, func(session *ports.ImplementationSession) error {
		session.Context = s.nextContext(session.Context, event)
		session.UpdatedAt = s.now()
		return nil
	})
}

// SetPhase transitions a session to phase, optionally recording message as a
// durable milestone event first.
func (s *ImplementationSessions) SetPhase(ctx context.Context, id string, phase ports.SessionPhase, message string) error {
	if s.store == nil {
		return errors.New("implementation session store is unavailable")
	}
	if strings.TrimSpace(message) != "" {
		if _, err := s.Append(ctx, id, ports.ImplementationSessionEvent{Kind: ports.SessionMilestone, Message: message}); err != nil {
			return err
		}
	}
	_, err := s.store.Update(ctx, id, func(session *ports.ImplementationSession) error {
		session.Phase = phase
		session.UpdatedAt = s.now()
		return nil
	})
	return err
}

// SetBranch records the branch and base revision a run committed to once its
// workspace branch is created, replacing direct access to the store.
func (s *ImplementationSessions) SetBranch(ctx context.Context, id, branch, baseRevision string) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) {
		session.Branch, session.BaseRevision = branch, baseRevision
	})
	return err
}

// ClearWorkspace records that a run's temporary workspace has been
// released, so no caller has to reach past this service into the underlying
// store.
func (s *ImplementationSessions) ClearWorkspace(ctx context.Context, id string) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) { session.Workspace = "" })
	return err
}

// RecordImplementation captures the diff and validation evidence an
// implementation run produced.
func (s *ImplementationSessions) RecordImplementation(ctx context.Context, id, diff, validation string) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) {
		session.Diff, session.Validation = diff, validation
	})
	return err
}

// RecordCommit captures the commit SHA shipping produced.
func (s *ImplementationSessions) RecordCommit(ctx context.Context, id, commit string) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) { session.Commit = commit })
	return err
}

// RecordPullRequest captures the pull request shipping created or reused.
func (s *ImplementationSessions) RecordPullRequest(ctx context.Context, id, url string, number int) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) {
		session.PullRequestURL, session.PullRequestNumber = url, number
	})
	return err
}

// RecordChecks captures the commit whose pull-request checks Eggy has
// already reacted to, and what they concluded. It is the dedupe key that
// keeps the checks loop from resuming the same failure every poll.
func (s *ImplementationSessions) RecordChecks(ctx context.Context, id, ref, conclusion string) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) {
		session.ChecksRef, session.ChecksConclusion = ref, conclusion
	})
	return err
}

// MarkFinished records the timestamp a run stopped actively progressing
// (completed, blocked, or interrupted).
func (s *ImplementationSessions) MarkFinished(ctx context.Context, id string, finishedAt time.Time) error {
	_, err := s.update(ctx, id, func(session *ports.ImplementationSession) { session.FinishedAt = finishedAt })
	return err
}

func (s *ImplementationSessions) update(ctx context.Context, id string, mutate func(*ports.ImplementationSession)) (ports.ImplementationSession, error) {
	if s.store == nil {
		return ports.ImplementationSession{}, errors.New("implementation session store is unavailable")
	}
	return s.store.Update(ctx, id, func(session *ports.ImplementationSession) error {
		mutate(session)
		session.UpdatedAt = s.now()
		return nil
	})
}

func (s *ImplementationSessions) MarkInterrupted(ctx context.Context) (int, error) {
	if s.store == nil {
		return 0, errors.New("implementation session store is unavailable")
	}
	sessions, err := s.store.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, session := range sessions {
		if session.Phase != ports.PhaseRunning {
			continue
		}
		// A turn transcript left running by a crash has nothing to resume:
		// there is no branch and no workspace behind it, only a record.
		if !IsCodingSession(session) {
			continue
		}
		if err := s.SetPhase(ctx, session.ID, ports.PhaseInterrupted, "Interrupted by restart; continue explicitly to resume."); err != nil {
			return count, err
		}
		if err := s.MarkFinished(ctx, session.ID, s.now()); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *ImplementationSessions) nextContext(context ports.SessionContext, event ports.ImplementationSessionEvent) ports.SessionContext {
	if event.Message != "" {
		context.Summary = agent.AppendSummary(context.Summary, event.Message)
	}
	if event.ModelMessage.Role != "" {
		context.RecentMessages = append(context.RecentMessages, agent.TruncateMessage(event.ModelMessage, s.policy.OutputExcerptChars))
	}
	for len(context.RecentMessages) > s.policy.RecentMessages || agent.MessageChars(context.RecentMessages) > s.policy.ContextBudgetChars {
		removed := context.RecentMessages[0]
		context.RecentMessages = context.RecentMessages[1:]
		context.Summary = agent.AppendSummary(context.Summary, agent.SummarizeMessage(removed))
	}
	return context
}

func (s *ImplementationSessions) sanitizeSession(session ports.ImplementationSession) ports.ImplementationSession {
	session.Instruction = s.redact(session.Instruction)
	session.Context.Summary = s.redact(session.Context.Summary)
	for i := range session.Context.RecentMessages {
		session.Context.RecentMessages[i] = s.sanitizeMessage(session.Context.RecentMessages[i])
	}
	return session
}

func (s *ImplementationSessions) sanitizeEvent(event ports.ImplementationSessionEvent) ports.ImplementationSessionEvent {
	event.Message = s.redact(event.Message)
	event.Content = truncateRunes(s.redact(event.Content), s.policy.OutputExcerptChars)
	event.ModelMessage = agent.TruncateMessage(s.sanitizeMessage(event.ModelMessage), s.policy.OutputExcerptChars)
	return event
}

func (s *ImplementationSessions) sanitizeMessage(message ports.Message) ports.Message {
	message.Content = s.redact(message.Content)
	for i := range message.ToolCalls {
		message.ToolCalls[i].Arguments = []byte(s.redact(string(message.ToolCalls[i].Arguments)))
	}
	return message
}

func (s *ImplementationSessions) redact(content string) string {
	return s.guard.Redact(content)
}

// RedactProgress removes configured credentials before implementation activity
// is exposed through a channel adapter.
func (s *ImplementationSessions) RedactProgress(content string) string { return s.redact(content) }

// truncateRunes is the services-local alias for the kernel's bounded
// truncation, kept so callers here (memory recall excerpts, session event
// sanitisation) read the same as they always did.
func truncateRunes(value string, limit int) string { return agent.TruncateRunes(value, limit) }
