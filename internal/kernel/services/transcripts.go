package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

type transcriptKey struct{}

// WithTranscript stamps the transcript recording this turn onto ctx, the
// same way a turn's destination travels. A tool deep in the turn (shipping's
// milestones) then records against the right transcript without every
// intermediate signature having to carry it.
func WithTranscript(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, transcriptKey{}, id)
}

// TranscriptOf returns the transcript recording this turn, or "" outside a
// turn (a CLI call, a test). Recording is best-effort by design: a missing
// transcript must never fail the owner's work.
func TranscriptOf(ctx context.Context) string {
	id, _ := ctx.Value(transcriptKey{}).(string)
	return id
}

// defaultExcerptChars bounds one event's contribution to a transcript when
// no explicit bound is configured.
const defaultExcerptChars = 8192

// Transcripts owns the durable, append-only record of what happened in a
// turn. It keeps no compacted context of its own: compaction belongs to
// agent.ContextPolicy, the one component that decides what a turn can still
// see. It has no lifecycle either -- a turn is over when it is over, and
// how far a *change* got is Changes' business.
type Transcripts struct {
	store        ports.TranscriptStore
	excerptChars int
	now          func() time.Time
	guard        *SecretGuard
}

func NewTranscripts(store ports.TranscriptStore, excerptChars int, now func() time.Time, activeSecrets ...string) *Transcripts {
	if now == nil {
		now = time.Now
	}
	if excerptChars <= 0 {
		excerptChars = defaultExcerptChars
	}
	return &Transcripts{store: store, excerptChars: excerptChars, now: now, guard: NewSecretGuard(activeSecrets)}
}

func (s *Transcripts) Open(ctx context.Context, id, instruction string) (ports.Transcript, error) {
	if s.store == nil {
		return ports.Transcript{}, errors.New("transcript store is unavailable")
	}
	if strings.TrimSpace(id) == "" {
		return ports.Transcript{}, errors.New("transcript id is required")
	}
	now := s.now()
	return s.store.Create(ctx, ports.Transcript{
		ID: id, Instruction: s.redact(instruction), StartedAt: now, UpdatedAt: now,
	})
}

func (s *Transcripts) Load(ctx context.Context, id string) (ports.Transcript, error) {
	if s.store == nil {
		return ports.Transcript{}, errors.New("transcript store is unavailable")
	}
	return s.store.Load(ctx, id)
}

// List returns every persisted transcript, most-recently-updated first.
func (s *Transcripts) List(ctx context.Context) ([]ports.Transcript, error) {
	if s.store == nil {
		return nil, errors.New("transcript store is unavailable")
	}
	return s.store.List(ctx)
}

// Append records one event, bounded and redacted.
func (s *Transcripts) Append(ctx context.Context, id string, event ports.TranscriptEvent) error {
	if s.store == nil {
		return errors.New("transcript store is unavailable")
	}
	event = s.sanitizeEvent(event)
	if event.At.IsZero() {
		event.At = s.now()
	}
	if _, err := s.store.AppendEvent(ctx, id, event); err != nil {
		return err
	}
	_, err := s.store.Update(ctx, id, func(transcript *ports.Transcript) error {
		transcript.UpdatedAt = s.now()
		return nil
	})
	return err
}

// Milestone records one durable, owner-readable note: commit created, branch
// pushed, ready to ship. These are things that happened, not states anything
// branches on.
func (s *Transcripts) Milestone(ctx context.Context, id, message string) error {
	if strings.TrimSpace(message) == "" || strings.TrimSpace(id) == "" {
		return nil
	}
	return s.Append(ctx, id, ports.TranscriptEvent{Kind: ports.SessionMilestone, Message: message})
}

// Close marks a transcript finished.
func (s *Transcripts) Close(ctx context.Context, id string) error {
	if s.store == nil {
		return errors.New("transcript store is unavailable")
	}
	_, err := s.store.Update(ctx, id, func(transcript *ports.Transcript) error {
		transcript.FinishedAt = s.now()
		transcript.UpdatedAt = transcript.FinishedAt
		return nil
	})
	return err
}

func (s *Transcripts) sanitizeEvent(event ports.TranscriptEvent) ports.TranscriptEvent {
	event.Message = s.redact(event.Message)
	event.Content = agent.TruncateRunes(s.redact(event.Content), s.excerptChars)
	event.ModelMessage = agent.TruncateMessage(s.sanitizeMessage(event.ModelMessage), s.excerptChars)
	return event
}

func (s *Transcripts) sanitizeMessage(message ports.Message) ports.Message {
	message.Content = s.redact(message.Content)
	for i := range message.ToolCalls {
		message.ToolCalls[i].Arguments = []byte(s.redact(string(message.ToolCalls[i].Arguments)))
	}
	return message
}

func (s *Transcripts) redact(content string) string { return s.guard.Redact(content) }

// RedactProgress removes configured credentials before activity is exposed
// through a channel adapter.
func (s *Transcripts) RedactProgress(content string) string { return s.redact(content) }

// truncateRunes is the services-local alias for the kernel's bounded
// truncation, kept so callers here (memory recall excerpts) read as they did.
func truncateRunes(value string, limit int) string { return agent.TruncateRunes(value, limit) }
