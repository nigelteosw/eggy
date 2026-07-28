package turns

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// turnTranscript is the durable record of one turn, plus the owner-facing
// milestone stream (`Inspected:`, `Edited:`, `Validation:`) when the turn is
// editing. When the thread already has an editing session, the turn appends
// to it, so inspect -> edit -> ship stays one continuous transcript. When it
// does not -- an ordinary conversation turn, a scheduled turn, a heartbeat --
// the turn opens a session of its own, which is what closes the gap where
// only editing turns were recorded at all.
type turnTranscript struct {
	service *Service
	// session is the transcript this turn appends to.
	session string
	// milestones is true only for an editing session: an ordinary chat turn
	// already reports itself through the "Calling ..." indicator, and
	// duplicating it as milestones would just be noise.
	milestones bool
}

// openTranscript builds this turn's transcript and the function that closes
// it. A nil Transcripts, or a transcript that cannot be opened, degrades to a
// no-op rather than failing the turn: recording is best-effort by design.
func (s *Service) openTranscript(ctx context.Context, instruction string) (*turnTranscript, func()) {
	if s.transcripts == nil {
		return nil, func() {}
	}
	// One transcript per turn, always: a turn that is editing simply also has
	// a change, which is a separate record with its own lifetime.
	id := newTranscriptID()
	if _, err := s.transcripts.Open(ctx, id, instruction); err != nil {
		s.logger.Error("turn transcript could not be opened", "transcript_id", id, "error", err)
		return nil, func() {}
	}
	transcript := &turnTranscript{service: s, session: id, milestones: s.editingSession(ctx) != ""}
	return transcript, func() { transcript.close(ctx) }
}

// Append records one loop event. It writes on a context detached from
// cancellation: a turn the owner stopped is exactly the turn whose record of
// what it had already done matters most.
func (t *turnTranscript) Append(ctx context.Context, event agent.Event) {
	if t == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if err := t.service.transcripts.Append(ctx, t.session, services.TurnTranscriptEvent(event)); err != nil {
		t.service.logger.Error("turn transcript append failed", "session_id", t.session, "error", err)
	}
	if !t.milestones || t.service.progress == nil {
		return
	}
	if message := services.TurnProgressMessage(event); message != "" {
		t.service.progress.Deliver(ctx, ports.CodingProgress{Kind: "milestone", Message: t.service.transcripts.RedactProgress(message), RunID: t.session})
	}
}

// Checkpoint records that the loop compacted its live context, with the
// summary that replaced the folded-away steps. The steps themselves are
// already in this transcript, so the checkpoint is a marker of what the model
// can still see, not a substitute for the record.
func (t *turnTranscript) Checkpoint(ctx context.Context, summary string) {
	if t == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if err := t.service.transcripts.Append(ctx, t.session, ports.TranscriptEvent{
		Kind: ports.SessionMilestone, Message: "Context checkpoint: compacted earlier steps of this turn", Content: summary,
	}); err != nil {
		t.service.logger.Error("turn transcript checkpoint failed", "session_id", t.session, "error", err)
	}
}

// close marks this turn's transcript finished. A transcript has no phase to
// settle: the turn either produced events or it did not.
func (t *turnTranscript) close(ctx context.Context) {
	if t == nil {
		return
	}
	if err := t.service.transcripts.Close(context.WithoutCancel(ctx), t.session); err != nil {
		t.service.logger.Error("turn transcript could not be closed", "transcript_id", t.session, "error", err)
	}
}

// editingSession returns the session recording this thread's edits, or ""
// when the thread has no branched workspace. Looking it up once per turn
// keeps the transcript wiring out of the loop itself.
func (s *Service) editingSession(ctx context.Context) string {
	if s.workspaces == nil {
		return ""
	}
	binding, err := s.workspaces.Resolve(ctx)
	if err != nil {
		return ""
	}
	return binding.Change
}
