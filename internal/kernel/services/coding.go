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

func (s *CodingService) Start(ctx context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	workspace, err := s.runner.Create(ctx, runID)
	if err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	run := ports.CodingRun{ID: runID, Repository: repository.Name, Workspace: workspace, Branch: repository.BaseBranch, Status: "running", StartedAt: s.now()}
	if s.sessions != nil {
		if _, err := s.sessions.Create(ctx, ports.ImplementationSession{ID: runID, Repository: repository.Name, Instruction: instruction, Workspace: workspace}); err != nil {
			_ = s.runner.Destroy(ctx, workspace)
			return ports.CodingRun{}, ports.CodingResult{}, err
		}
		if err := s.sessions.SetStatus(ctx, runID, ports.SessionRunning, ""); err != nil {
			return ports.CodingRun{}, ports.CodingResult{}, err
		}
	}
	report := s.reporter(ctx, runID, progress)
	checkpoint(report, "Preparing an isolated workspace for "+repository.Name)
	if err := s.persistRun(ctx, run); err != nil {
		_ = s.runner.Destroy(ctx, workspace)
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	fail := func(cause error) (ports.CodingRun, ports.CodingResult, error) {
		run.Status, run.FinishedAt = "failed", s.now()
		_ = s.persistRun(ctx, run)
		if s.sessions != nil {
			_ = s.sessions.SetStatus(ctx, runID, ports.SessionBlocked, "Blocked: "+cause.Error())
		}
		return run, ports.CodingResult{}, cause
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
	run.Branch = branch
	expectedRevision, err := s.workspaceRevision(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	if expectedRevision.Branch != branch {
		return fail(fmt.Errorf("repository created unexpected branch %q", expectedRevision.Branch))
	}
	run.BaseRevision = expectedRevision.Head
	if err := s.persistRun(ctx, run); err != nil {
		return fail(err)
	}
	if s.sessions != nil {
		if _, err := s.sessions.store.Update(ctx, runID, func(session *ports.ImplementationSession) error {
			session.Branch, session.BaseRevision = run.Branch, run.BaseRevision
			session.UpdatedAt = s.now()
			return nil
		}); err != nil {
			return fail(err)
		}
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
	run.Status, run.Diff, run.Validation, run.FinishedAt = "completed", diff, result.Validation, s.now()
	if err := s.persistRun(ctx, run); err != nil {
		return run, result, err
	}
	if s.sessions != nil {
		if err := s.sessions.SetStatus(ctx, runID, ports.SessionAwaitingCommitApproval, "Ready for commit approval"); err != nil {
			return run, result, err
		}
	}
	return run, result, nil
}

func (s *CodingService) Resume(ctx context.Context, runID, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	if s.sessions == nil {
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("implementation sessions are unavailable")
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	run, ok := state.CodingRuns[runID]
	if !ok {
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("coding run not found")
	}
	repository, ok := state.Repositories[run.Repository]
	if !ok {
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("repository is not registered")
	}
	history, session, err := s.sessions.ResumeContext(ctx, runID)
	if err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	if session.Repository != repository.Name || session.Workspace != run.Workspace || session.Branch != run.Branch || session.BaseRevision != run.BaseRevision {
		_ = s.sessions.SetStatus(ctx, runID, ports.SessionBlocked, "Blocked: persisted session and coding run no longer match")
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("persisted session and coding run no longer match")
	}
	revision, err := s.workspaceRevision(ctx, run.Workspace)
	if err != nil || revision.Branch != run.Branch || revision.Head != run.BaseRevision {
		_ = s.sessions.SetStatus(ctx, runID, ports.SessionBlocked, "Blocked: persisted workspace or branch is unavailable")
		if err != nil {
			return ports.CodingRun{}, ports.CodingResult{}, err
		}
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("persisted workspace or branch no longer matches the session")
	}
	if session.Status == ports.SessionAwaitingCommitApproval && s.invalidator != nil {
		if err := s.invalidator.InvalidatePendingCommitForRun(ctx, runID); err != nil {
			return ports.CodingRun{}, ports.CodingResult{}, err
		}
	}
	run.Status, run.FinishedAt = "running", time.Time{}
	if err := s.persistRun(ctx, run); err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	if err := s.sessions.SetStatus(ctx, runID, ports.SessionRunning, ""); err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	report := s.reporter(ctx, runID, progress)
	guidance, err := s.repository.Inspect(ctx, run.Workspace)
	if err != nil {
		return s.resumeFailure(ctx, run, err)
	}
	prompt := modifyingRunnerContract + "\n\nContinue task:\n" + instruction
	if guidance != "" {
		prompt = fmt.Sprintf("%s\n\nRepository guidance from AGENTS.md:\n%s\n\nContinue task:\n%s", modifyingRunnerContract, guidance, instruction)
	}
	result, err := s.implement(ctx, ImplementationRequest{RunID: runID, Workspace: run.Workspace, Instruction: prompt, History: history}, report)
	if err != nil {
		return s.resumeFailure(ctx, run, err)
	}
	actual, err := s.workspaceRevision(ctx, run.Workspace)
	if err != nil || actual.Branch != run.Branch || actual.Head != run.BaseRevision {
		if err == nil {
			err = errors.New("coding agent changed branch or HEAD before commit approval")
		}
		return s.resumeFailure(ctx, run, err)
	}
	diff, err := s.repository.Diff(ctx, run.Workspace)
	if err != nil {
		return s.resumeFailure(ctx, run, err)
	}
	run.Status, run.Diff, run.Validation, run.FinishedAt = "completed", diff, result.Validation, s.now()
	if err := s.persistRun(ctx, run); err != nil {
		return run, result, err
	}
	if err := s.sessions.SetStatus(ctx, runID, ports.SessionAwaitingCommitApproval, "Ready for commit approval"); err != nil {
		return run, result, err
	}
	return run, result, nil
}

// ResumeLatest resumes the most recently updated session that can safely be
// continued. It is used by the explicit owner command when no run ID is given.
func (s *CodingService) ResumeLatest(ctx context.Context, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	if s.sessions == nil {
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("implementation sessions are unavailable")
	}
	sessions, err := s.sessions.ListResumable(ctx)
	if err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	if len(sessions) == 0 {
		return ports.CodingRun{}, ports.CodingResult{}, errors.New("no resumable coding sessions")
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

func (s *CodingService) resumeFailure(ctx context.Context, run ports.CodingRun, cause error) (ports.CodingRun, ports.CodingResult, error) {
	run.Status, run.FinishedAt = "failed", s.now()
	_ = s.persistRun(ctx, run)
	_ = s.sessions.SetStatus(ctx, run.ID, ports.SessionBlocked, "Blocked: "+cause.Error())
	return run, ports.CodingResult{}, cause
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
	state, err := s.store.Load(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		for id, run := range state.CodingRuns {
			if run.Status != "running" {
				continue
			}
			run.Status, run.FinishedAt = "interrupted", s.now()
			state.CodingRuns[id] = run
			count++
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	if s.sessions != nil {
		if _, err := s.sessions.MarkInterrupted(ctx); err != nil {
			return count, err
		}
	}
	return count, nil
}

func (s *CodingService) Cleanup(ctx context.Context, runID string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	run, ok := state.CodingRuns[runID]
	if !ok {
		return fmt.Errorf("coding run %q not found", runID)
	}
	if run.Workspace != "" {
		if err := s.runner.Destroy(ctx, run.Workspace); err != nil {
			return err
		}
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		updated := state.CodingRuns[runID]
		updated.Workspace = ""
		state.CodingRuns[runID] = updated
		return nil
	})
	return err
}

func (s *CodingService) CleanupExpired(ctx context.Context, cutoff time.Time) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0)
	for id, run := range state.CodingRuns {
		if run.Workspace != "" && !run.FinishedAt.IsZero() && run.FinishedAt.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if err := s.Cleanup(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *CodingService) persistRun(ctx context.Context, run ports.CodingRun) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.CodingRuns == nil {
			state.CodingRuns = map[string]ports.CodingRun{}
		}
		state.CodingRuns[run.ID] = run
		return nil
	})
	return err
}
