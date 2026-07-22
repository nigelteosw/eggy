package services

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nigelteosw/eggy/internal/ports"
)

const sessionSummaryLimit = 4096

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

func (s *ImplementationSessions) ListResumable(ctx context.Context) ([]ports.ImplementationSession, error) {
	if s.store == nil {
		return nil, errors.New("implementation session store is unavailable")
	}
	sessions, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ports.ImplementationSession, 0, len(sessions))
	for _, session := range sessions {
		if resumablePhase(session.Phase) {
			result = append(result, session)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

// List returns every persisted session, most-recently-updated first.
func (s *ImplementationSessions) List(ctx context.Context) ([]ports.ImplementationSession, error) {
	if s.store == nil {
		return nil, errors.New("implementation session store is unavailable")
	}
	return s.store.List(ctx)
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

func (s *ImplementationSessions) ResumeContext(ctx context.Context, id string) ([]ports.Message, ports.ImplementationSession, error) {
	session, err := s.Load(ctx, id)
	if err != nil {
		return nil, ports.ImplementationSession{}, err
	}
	if !resumablePhase(session.Phase) {
		return nil, ports.ImplementationSession{}, errors.New("implementation session is not resumable")
	}
	messages := make([]ports.Message, 0, len(session.Context.RecentMessages)+1)
	if session.Context.Summary != "" {
		messages = append(messages, ports.Message{Role: ports.RoleSystem, Content: "Previous implementation session:\n" + session.Context.Summary})
	}
	messages = append(messages, session.Context.RecentMessages...)
	return messages, session, nil
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
// destroyed, so CodingService.Cleanup never has to reach past this service
// into the underlying store.
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

func resumablePhase(phase ports.SessionPhase) bool {
	switch phase {
	case ports.PhaseInterrupted, ports.PhaseBlocked, ports.PhaseReady:
		return true
	default:
		return false
	}
}

func (s *ImplementationSessions) nextContext(context ports.SessionContext, event ports.ImplementationSessionEvent) ports.SessionContext {
	if event.Message != "" {
		context.Summary = appendSummary(context.Summary, event.Message)
	}
	if event.ModelMessage.Role != "" {
		context.RecentMessages = append(context.RecentMessages, truncateMessage(event.ModelMessage, s.policy.OutputExcerptChars))
	}
	for len(context.RecentMessages) > s.policy.RecentMessages || messageChars(context.RecentMessages) > s.policy.ContextBudgetChars {
		removed := context.RecentMessages[0]
		context.RecentMessages = context.RecentMessages[1:]
		context.Summary = appendSummary(context.Summary, summarizeMessage(removed))
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
	event.ModelMessage = truncateMessage(s.sanitizeMessage(event.ModelMessage), s.policy.OutputExcerptChars)
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

func appendSummary(summary, event string) string {
	event = truncateRunes(strings.TrimSpace(event), 320)
	if event == "" {
		return summary
	}
	if summary == "" {
		return event
	}
	return truncateRunes(summary+"\n"+event, sessionSummaryLimit)
}

func summarizeMessage(message ports.Message) string {
	if message.Name != "" {
		return "Used " + message.Name
	}
	if message.Content != "" {
		return truncateRunes(message.Content, 160)
	}
	return "Recorded implementation activity"
}

func truncateMessage(message ports.Message, limit int) ports.Message {
	message.Content = truncateRunes(message.Content, limit)
	return message
}

func messageChars(messages []ports.Message) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
		for _, call := range message.ToolCalls {
			total += utf8.RuneCountInString(string(call.Arguments))
		}
	}
	return total
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
