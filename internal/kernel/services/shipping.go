package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

type ShippingService struct {
	store        ports.StateStore
	policy       ports.ApprovalPolicy
	provider     ports.RepositoryProvider
	repositories map[string]ports.Repository
	requester    ApprovalRequester
}

func (s *ShippingService) SetApprovalRequester(requester ApprovalRequester) { s.requester = requester }

func (s *ShippingService) RequestCommit(ctx context.Context, runID, message string) (approvals.Approval, error) {
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := commitPayload{RunID: runID, Diff: run.Diff, Message: message}
	return s.requester.Request(ctx, approvals.Commit, payload, "Commit changes for "+runID)
}

func (s *ShippingService) RequestPush(ctx context.Context, runID, branch string) (approvals.Approval, error) {
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := pushPayload{RunID: runID, Branch: branch, Commit: run.Commit}
	return s.requester.Request(ctx, approvals.Push, payload, "Push "+branch)
}

func (s *ShippingService) RequestPullRequest(ctx context.Context, runID, branch, title, body string) (approvals.Approval, error) {
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := pullRequestPayload{RunID: runID, Branch: branch, Commit: run.Commit, Title: title, Body: body}
	return s.requester.Request(ctx, approvals.CreatePR, payload, "Create pull request for "+branch)
}

func (s *ShippingService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	switch approval.Action {
	case approvals.Commit:
		var payload commitPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return s.Commit(ctx, payload.RunID, payload.Message, approval.ID)
	case approvals.Push:
		var payload pushPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return nil, s.Push(ctx, payload.RunID, payload.Branch, approval.ID)
	case approvals.CreatePR:
		var payload pullRequestPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return s.CreatePullRequest(ctx, payload.RunID, payload.Branch, payload.Title, payload.Body, approval.ID)
	default:
		return nil, errors.New("approval is not a shipping action")
	}
}

func NewShippingService(store ports.StateStore, policy ports.ApprovalPolicy, provider ports.RepositoryProvider, repositories map[string]ports.Repository) *ShippingService {
	return &ShippingService{store: store, policy: policy, provider: provider, repositories: repositories}
}

type commitPayload struct{ RunID, Diff, Message string }
type pushPayload struct{ RunID, Branch, Commit string }
type pullRequestPayload struct{ RunID, Branch, Commit, Title, Body string }

func (s *ShippingService) Commit(ctx context.Context, runID, message, approvalID string) (string, error) {
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return "", err
	}
	currentDiff, err := s.provider.Diff(ctx, run.Workspace)
	if err != nil {
		return "", err
	}
	if currentDiff != run.Diff {
		return "", approvals.ErrPayloadChanged
	}
	payload := commitPayload{RunID: runID, Diff: run.Diff, Message: message}
	if err := s.policy.Authorize(ctx, approvals.Commit, payload, approvalID); err != nil {
		return "", err
	}
	commit, err := s.provider.Commit(ctx, run.Workspace, message)
	if err != nil {
		return "", err
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return "", err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		updated := state.CodingRuns[runID]
		updated.Commit = commit
		state.CodingRuns[runID] = updated
		return nil
	})
	return commit, err
}

func (s *ShippingService) Push(ctx context.Context, runID, branch, approvalID string) error {
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return err
	}
	payload := pushPayload{RunID: runID, Branch: branch, Commit: run.Commit}
	head, err := s.provider.Head(ctx, run.Workspace)
	if err != nil {
		return err
	}
	if run.Commit == "" || head != run.Commit {
		return approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.Push, payload, approvalID); err != nil {
		return err
	}
	return s.provider.Push(ctx, run.Workspace, branch)
}

func (s *ShippingService) CreatePullRequest(ctx context.Context, runID, branch, title, body, approvalID string) (ports.PullRequest, error) {
	run, repository, err := s.run(ctx, runID)
	if err != nil {
		return ports.PullRequest{}, err
	}
	payload := pullRequestPayload{RunID: runID, Branch: branch, Commit: run.Commit, Title: title, Body: body}
	remoteHead, err := s.provider.RemoteHead(ctx, run.Workspace, branch)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if run.Commit == "" || remoteHead != run.Commit {
		return ports.PullRequest{}, approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.CreatePR, payload, approvalID); err != nil {
		return ports.PullRequest{}, err
	}
	return s.provider.CreatePullRequest(ctx, repository, branch, title, body)
}

func (s *ShippingService) run(ctx context.Context, id string) (ports.CodingRun, ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.CodingRun{}, ports.Repository{}, err
	}
	run, ok := state.CodingRuns[id]
	if !ok {
		return ports.CodingRun{}, ports.Repository{}, errors.New("coding run not found")
	}
	repository, ok := s.repositories[run.Repository]
	if !ok {
		return ports.CodingRun{}, ports.Repository{}, errors.New("repository is not registered")
	}
	return run, repository, nil
}
