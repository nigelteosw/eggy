package repo

import (
	"context"
	"errors"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Changes owns the record of branched, shippable work: what a thread's
// checkout was branched to do, and how far it got. It is deliberately
// separate from services.Transcripts. A change has a lifecycle and no event log; a
// transcript has an event log and no lifecycle. Fusing them was what forced
// callers to ask "is this session really a coding run?" by inspecting which
// fields happened to be populated.
type Changes struct {
	store ports.ChangeStore
	now   func() time.Time
	guard *services.SecretGuard
}

func NewChanges(store ports.ChangeStore, now func() time.Time, activeSecrets ...string) *Changes {
	if now == nil {
		now = time.Now
	}
	return &Changes{store: store, now: now, guard: services.NewSecretGuard(activeSecrets)}
}

// Open records a new change for the branch workspace_edit just created. The
// model alias is the caller's, captured now: a change outlives the selection
// that opened it, so reading it back later would report whatever /model
// happens to be set to rather than what produced the diff.
func (s *Changes) Open(ctx context.Context, id, repository, branch, baseRevision, model string) (ports.Change, error) {
	if s.store == nil {
		return ports.Change{}, errors.New("change store is unavailable")
	}
	if strings.TrimSpace(id) == "" {
		return ports.Change{}, errors.New("change id is required")
	}
	now := s.now()
	return s.store.Create(ctx, ports.Change{
		ID: id, Repository: repository, Branch: branch, BaseRevision: baseRevision,
		Model:      model,
		Unprompted: services.IsUnpromptedTurn(ctx),
		Phase:      ports.PhaseRunning, StartedAt: now, UpdatedAt: now,
	})
}

func (s *Changes) Load(ctx context.Context, id string) (ports.Change, error) {
	if s.store == nil {
		return ports.Change{}, errors.New("change store is unavailable")
	}
	return s.store.Load(ctx, id)
}

// List returns every change, most-recently-updated first. This is what
// /runs shows: no filtering, because a change is only ever created by
// workspace_edit branching a checkout.
func (s *Changes) List(ctx context.Context) ([]ports.Change, error) {
	if s.store == nil {
		return nil, errors.New("change store is unavailable")
	}
	return s.store.List(ctx)
}

// SetPhase moves a change through the only three states anything branches
// on. The reason belongs on the thread's transcript as a milestone.
func (s *Changes) SetPhase(ctx context.Context, id string, phase ports.SessionPhase) error {
	return s.update(ctx, id, func(change *ports.Change) {
		change.Phase = phase
		if phase != ports.PhaseRunning {
			change.FinishedAt = s.now()
		}
	})
}

// Rebase records the branch's new baseline after a commit lands, so a second
// round of edits verifies against where the branch actually is now.
func (s *Changes) Rebase(ctx context.Context, id, baseRevision string) error {
	return s.update(ctx, id, func(change *ports.Change) { change.BaseRevision = baseRevision })
}

// RecordImplementation captures the diff and validation evidence.
func (s *Changes) RecordImplementation(ctx context.Context, id, diff, validation string) error {
	return s.update(ctx, id, func(change *ports.Change) {
		change.Diff, change.Validation = s.guard.Redact(diff), s.guard.Redact(validation)
	})
}

// RecordCommit captures the commit SHA shipping produced.
func (s *Changes) RecordCommit(ctx context.Context, id, commit string) error {
	return s.update(ctx, id, func(change *ports.Change) { change.Commit = commit })
}

// RecordPullRequest captures the pull request shipping created or reused.
func (s *Changes) RecordPullRequest(ctx context.Context, id, url string, number int) error {
	return s.update(ctx, id, func(change *ports.Change) {
		change.PullRequestURL, change.PullRequestNumber = url, number
	})
}

// RecordChecks captures the commit whose pull-request checks Eggy has
// already reacted to. It is the dedupe key that keeps the checks loop from
// resuming the same failure on every poll.
func (s *Changes) RecordChecks(ctx context.Context, id, ref, conclusion string) error {
	return s.update(ctx, id, func(change *ports.Change) {
		change.ChecksRef, change.ChecksConclusion = ref, conclusion
	})
}

// MarkInterrupted blocks every change left running by a restart. A change is
// the only thing with a lifecycle to recover: a transcript interrupted
// mid-turn is simply a transcript that stops there.
func (s *Changes) MarkInterrupted(ctx context.Context) (int, error) {
	changes, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, change := range changes {
		if change.Phase != ports.PhaseRunning {
			continue
		}
		if err := s.SetPhase(ctx, change.ID, ports.PhaseBlocked); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *Changes) update(ctx context.Context, id string, mutate func(*ports.Change)) error {
	if s.store == nil {
		return errors.New("change store is unavailable")
	}
	_, err := s.store.Update(ctx, id, func(change *ports.Change) error {
		mutate(change)
		change.UpdatedAt = s.now()
		return nil
	})
	return err
}
