package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type CodingService struct {
	store      ports.StateStore
	runner     ports.Runner
	repository ports.CodingRepository
	agent      ports.CodingAgent
	now        func() time.Time
}

func NewCodingService(store ports.StateStore, runner ports.Runner, repository ports.CodingRepository, agent ports.CodingAgent, now func() time.Time) *CodingService {
	return &CodingService{store: store, runner: runner, repository: repository, agent: agent, now: now}
}

func (s *CodingService) Start(ctx context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	checkpoint(progress, "Preparing an isolated workspace for "+repository.Name)
	workspace, err := s.runner.Create(ctx, runID)
	if err != nil {
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	run := ports.CodingRun{ID: runID, Repository: repository.Name, Workspace: workspace, Branch: repository.BaseBranch, Status: "running", StartedAt: s.now()}
	if err := s.persistRun(ctx, run); err != nil {
		_ = s.runner.Destroy(ctx, workspace)
		return ports.CodingRun{}, ports.CodingResult{}, err
	}
	fail := func(cause error) (ports.CodingRun, ports.CodingResult, error) {
		run.Status, run.FinishedAt = "failed", s.now()
		_ = s.persistRun(ctx, run)
		return run, ports.CodingResult{}, cause
	}
	checkpoint(progress, "Cloning "+repository.Name+"@"+repository.BaseBranch)
	if err := s.repository.Clone(ctx, repository, workspace); err != nil {
		return fail(err)
	}
	branch := "eggy/" + runID
	checkpoint(progress, "Creating branch "+branch)
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
	guidance, err := s.repository.Inspect(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	prompt := modifyingRunnerContract + "\n\nTask:\n" + instruction
	if guidance != "" {
		prompt = fmt.Sprintf("%s\n\nRepository guidance from AGENTS.md:\n%s\n\nTask:\n%s", modifyingRunnerContract, guidance, instruction)
	}
	checkpoint(progress, "Starting the coding agent")
	result, err := s.agent.Run(ctx, ports.CodingRequest{RunID: runID, Workspace: workspace, Instruction: prompt}, progress)
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
	checkpoint(progress, "Capturing diff and validation evidence")
	diff, err := s.repository.Diff(ctx, workspace)
	if err != nil {
		return fail(err)
	}
	run.Status, run.Diff, run.Validation, run.FinishedAt = "completed", diff, result.Validation, s.now()
	if err := s.persistRun(ctx, run); err != nil {
		return run, result, err
	}
	return run, result, nil
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

func (s *CodingService) Stop(runID string) error { return s.agent.Interrupt(runID) }

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
	return count, err
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
