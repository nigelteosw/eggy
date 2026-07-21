package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type CodingService struct {
	store       ports.StateStore
	runner      ports.Runner
	repository  ports.CodingRepository
	implementer Implementer
	sessions    *ImplementationSessions
	invalidator PendingCommitApprovalInvalidator
	now         func() time.Time
}

type PendingCommitApprovalInvalidator interface {
	InvalidatePendingCommitForRun(context.Context, string) error
}

func NewCodingService(store ports.StateStore, runner ports.Runner, repository ports.CodingRepository, implementer Implementer, now func() time.Time, sessions *ImplementationSessions, invalidators ...PendingCommitApprovalInvalidator) *CodingService {
	service := &CodingService{store: store, runner: runner, repository: repository, implementer: implementer, sessions: sessions, now: now}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
}

func (s *CodingService) requireSessions() error {
	if s.sessions == nil {
		return errors.New("implementation sessions are unavailable")
	}
	return nil
}

func (s *CodingService) Start(ctx context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.ImplementationSession, ports.CodingResult, error) {
	if err := s.requireSessions(); err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	workspace, err := s.runner.Create(ctx, runID)
	if err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	session, err := s.sessions.Create(ctx, ports.ImplementationSession{ID: runID, Repository: repository.Name, Instruction: instruction, Workspace: workspace, Phase: ports.PhaseRunning})
	if err != nil {
		_ = s.runner.Destroy(ctx, workspace)
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	report := s.reporter(ctx, runID, progress)
	checkpoint(report, "Preparing an isolated workspace for "+repository.Name)
	fail := func(cause error) (ports.ImplementationSession, ports.CodingResult, error) {
		_ = s.sessions.SetPhase(ctx, runID, ports.PhaseBlocked, "Blocked: "+cause.Error())
		_ = s.sessions.MarkFinished(ctx, runID, s.now())
		return session, ports.CodingResult{}, cause
	}
	checkpoint(report, "Cloning "+repository.Name+"@"+repository.BaseBranch)
	if err := s.repository.Clone(ctx, repository, workspace); err != nil {
		return fail(err)
	}
	branch := "eggy/" + runID
	checkpoint(report, "Creating branch "+branch)
	if err := s.repository.CreateBranch(ctx, workspace, branch); err != nil {
		return fail(err)
	}
	expectedRevision, err := s.workspaceRevision(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	if expectedRevision.Branch != branch {
		return fail(fmt.Errorf("repository created unexpected branch %q", expectedRevision.Branch))
	}
	if err := s.sessions.SetBranch(ctx, runID, branch, expectedRevision.Head); err != nil {
		return fail(err)
	}
	guidance, err := s.repository.Inspect(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	prompt := modifyingRunnerContract + "\n\nTask:\n" + instruction
	if guidance != "" {
		prompt = fmt.Sprintf("%s\n\nRepository guidance from AGENTS.md:\n%s\n\nTask:\n%s", modifyingRunnerContract, guidance, instruction)
	}
	checkpoint(report, "Starting the implementation run")
	result, err := s.implement(ctx, ImplementationRequest{RunID: runID, Workspace: workspace, Instruction: prompt}, report)
	if err != nil {
		return fail(err)
	}
	actualRevision, err := s.workspaceRevision(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	if actualRevision.Branch != expectedRevision.Branch {
		return fail(fmt.Errorf("coding agent changed branch from %q to %q", expectedRevision.Branch, actualRevision.Branch))
	}
	if actualRevision.Head != expectedRevision.Head {
		return fail(errors.New("coding agent changed HEAD before commit approval"))
	}
	checkpoint(report, "Capturing diff and validation evidence")
	diff, err := s.repository.Diff(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	if err := s.sessions.RecordImplementation(ctx, runID, diff, result.Validation); err != nil {
		return session, result, err
	}
	if err := s.sessions.SetPhase(ctx, runID, ports.PhaseReady, "Ready to ship"); err != nil {
		return session, result, err
	}
	session, err = s.sessions.Load(ctx, runID)
	if err != nil {
		return session, result, err
	}
	return session, result, nil
}

func (s *CodingService) Resume(ctx context.Context, runID, instruction string, progress func(ports.CodingProgress)) (ports.ImplementationSession, ports.CodingResult, error) {
	if err := s.requireSessions(); err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	history, session, err := s.sessions.ResumeContext(ctx, runID)
	if err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	if _, ok := state.Repositories[session.Repository]; !ok {
		return ports.ImplementationSession{}, ports.CodingResult{}, errors.New("repository is not registered")
	}
	revision, err := s.workspaceRevision(ctx, session.Workspace)
	if err != nil || revision.Branch != session.Branch || revision.Head != session.BaseRevision {
		_ = s.sessions.SetPhase(ctx, runID, ports.PhaseBlocked, "Blocked: persisted workspace or branch is unavailable")
		_ = s.sessions.MarkFinished(ctx, runID, s.now())
		if err != nil {
			return ports.ImplementationSession{}, ports.CodingResult{}, err
		}
		return ports.ImplementationSession{}, ports.CodingResult{}, errors.New("persisted workspace or branch no longer matches the session")
	}
	if session.Phase == ports.PhaseReady && s.invalidator != nil {
		if err := s.invalidator.InvalidatePendingCommitForRun(ctx, runID); err != nil {
			return ports.ImplementationSession{}, ports.CodingResult{}, err
		}
	}
	if err := s.sessions.SetPhase(ctx, runID, ports.PhaseRunning, ""); err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	report := s.reporter(ctx, runID, progress)
	guidance, err := s.repository.Inspect(ctx, session.Workspace)
	if err != nil {
		return s.resumeFailure(ctx, session, err)
	}
	prompt := modifyingRunnerContract + "\n\nContinue task:\n" + instruction
	if guidance != "" {
		prompt = fmt.Sprintf("%s\n\nRepository guidance from AGENTS.md:\n%s\n\nContinue task:\n%s", modifyingRunnerContract, guidance, instruction)
	}
	result, err := s.implement(ctx, ImplementationRequest{RunID: runID, Workspace: session.Workspace, Instruction: prompt, History: history}, report)
	if err != nil {
		return s.resumeFailure(ctx, session, err)
	}
	actual, err := s.workspaceRevision(ctx, session.Workspace)
	if err != nil || actual.Branch != session.Branch || actual.Head != session.BaseRevision {
		if err == nil {
			err = errors.New("coding agent changed branch or HEAD before commit approval")
		}
		return s.resumeFailure(ctx, session, err)
	}
	diff, err := s.repository.Diff(ctx, session.Workspace)
	if err != nil {
		return s.resumeFailure(ctx, session, err)
	}
	if err := s.sessions.RecordImplementation(ctx, runID, diff, result.Validation); err != nil {
		return session, result, err
	}
	if err := s.sessions.SetPhase(ctx, runID, ports.PhaseReady, "Ready to ship"); err != nil {
		return session, result, err
	}
	session, err = s.sessions.Load(ctx, runID)
	if err != nil {
		return session, result, err
	}
	return session, result, nil
}

// ResumeLatest resumes the most recently updated session that can safely be
// continued. It is used by the explicit owner command when no run ID is given.
func (s *CodingService) ResumeLatest(ctx context.Context, instruction string, progress func(ports.CodingProgress)) (ports.ImplementationSession, ports.CodingResult, error) {
	if err := s.requireSessions(); err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	sessions, err := s.sessions.ListResumable(ctx)
	if err != nil {
		return ports.ImplementationSession{}, ports.CodingResult{}, err
	}
	if len(sessions) == 0 {
		return ports.ImplementationSession{}, ports.CodingResult{}, errors.New("no resumable coding sessions")
	}
	return s.Resume(ctx, sessions[0].ID, instruction, progress)
}

func (s *CodingService) implement(ctx context.Context, request ImplementationRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	var record func(ports.ImplementationSessionEvent)
	if s.sessions != nil {
		record = func(event ports.ImplementationSessionEvent) { _, _ = s.sessions.Append(ctx, request.RunID, event) }
	}
	return s.implementer.Implement(ctx, request, record, progress)
}

func (s *CodingService) reporter(ctx context.Context, runID string, progress func(ports.CodingProgress)) func(ports.CodingProgress) {
	return func(event ports.CodingProgress) {
		event.RunID = runID
		if s.sessions != nil {
			event.Message = s.sessions.RedactProgress(event.Message)
		}
		if s.sessions != nil && event.Message != "" {
			_, _ = s.sessions.Append(ctx, runID, ports.ImplementationSessionEvent{Kind: ports.SessionMilestone, Message: event.Message})
		}
		if progress != nil {
			progress(event)
		}
	}
}

func (s *CodingService) resumeFailure(ctx context.Context, session ports.ImplementationSession, cause error) (ports.ImplementationSession, ports.CodingResult, error) {
	_ = s.sessions.SetPhase(ctx, session.ID, ports.PhaseBlocked, "Blocked: "+cause.Error())
	_ = s.sessions.MarkFinished(ctx, session.ID, s.now())
	return session, ports.CodingResult{}, cause
}

func checkpoint(progress func(ports.CodingProgress), message string) {
	if progress == nil {
		return
	}
	progress(ports.CodingProgress{Kind: "checkpoint", Message: message})
}

const modifyingRunnerContract = `Eggy runner contract:
- Work only in the current checkout and remain on the current branch.
- Do not create, switch, rename, or delete branches.
- Do not commit, push, or create pull requests; Eggy performs each action only after its independent owner approval.
- Make the requested file changes and run validation, then return the requested structured result.`

func (s *CodingService) workspaceRevision(ctx context.Context, workspace string) (ports.WorkspaceRevision, error) {
	return s.repository.WorkspaceRevision(ctx, workspace)
}

func (s *CodingService) Stop(runID string) error { return s.implementer.Interrupt(runID) }

func (s *CodingService) RecoverInterrupted(ctx context.Context) (int, error) {
	if s.sessions == nil {
		return 0, nil
	}
	return s.sessions.MarkInterrupted(ctx)
}

// List returns every coding run's canonical session record, for status and
// /runs reporting.
func (s *CodingService) List(ctx context.Context) ([]ports.ImplementationSession, error) {
	if s.sessions == nil {
		return nil, nil
	}
	return s.sessions.List(ctx)
}

func (s *CodingService) Cleanup(ctx context.Context, runID string) error {
	if err := s.requireSessions(); err != nil {
		return err
	}
	session, err := s.sessions.Load(ctx, runID)
	if err != nil {
		return fmt.Errorf("coding run %q not found: %w", runID, err)
	}
	if session.Workspace != "" {
		if err := s.runner.Destroy(ctx, session.Workspace); err != nil {
			return err
		}
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
