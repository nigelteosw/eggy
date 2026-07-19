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
	workspace    ports.WorkspaceInspector
	committer    ports.RepositoryCommitter
	pusher       ports.RepositoryPusher
	pullRequests ports.PullRequestProvider
	capabilities ports.RepositoryCapabilities
	requester    ApprovalRequester
}

var (
	ErrRepositoryCommitUnavailable = errors.New("repository commit capability is unavailable")
	ErrRepositoryPushUnavailable   = errors.New("repository push capability is unavailable")
	ErrPullRequestUnavailable      = errors.New("pull-request capability is unavailable")
)

func (s *ShippingService) SetApprovalRequester(requester ApprovalRequester) { s.requester = requester }

func (s *ShippingService) RequestCommit(ctx context.Context, runID, message string) (approvals.Approval, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return approvals.Approval{}, ErrRepositoryCommitUnavailable
	}
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := commitPayload{RunID: runID, Branch: run.Branch, BaseRevision: run.BaseRevision, Diff: run.Diff, Message: message}
	return s.requester.Request(ctx, approvals.Commit, payload, "Commit changes for "+runID)
}

func (s *ShippingService) RequestPush(ctx context.Context, runID, branch string) (approvals.Approval, error) {
	if !s.capabilities.Push || s.pusher == nil {
		return approvals.Approval{}, ErrRepositoryPushUnavailable
	}
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
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return approvals.Approval{}, ErrPullRequestUnavailable
	}
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

func NewShippingService(store ports.StateStore, policy ports.ApprovalPolicy, workspace ports.WorkspaceInspector, committer ports.RepositoryCommitter, pusher ports.RepositoryPusher, pullRequests ports.PullRequestProvider, capabilities ports.RepositoryCapabilities) *ShippingService {
	return &ShippingService{store: store, policy: policy, workspace: workspace, committer: committer, pusher: pusher, pullRequests: pullRequests, capabilities: capabilities}
}

type commitPayload struct{ RunID, Branch, BaseRevision, Diff, Message string }
type pushPayload struct{ RunID, Branch, Commit string }
type pullRequestPayload struct{ RunID, Branch, Commit, Title, Body string }

func (s *ShippingService) Commit(ctx context.Context, runID, message, approvalID string) (string, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return "", ErrRepositoryCommitUnavailable
	}
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return "", err
	}
	currentRevision, err := s.workspace.WorkspaceRevision(ctx, run.Workspace)
	if err != nil {
		return "", err
	}
	if run.Branch == "" || run.BaseRevision == "" || currentRevision.Branch != run.Branch || currentRevision.Head != run.BaseRevision {
		return "", approvals.ErrPayloadChanged
	}
	currentDiff, err := s.committer.Diff(ctx, run.Workspace)
	if err != nil {
		return "", err
	}
	if currentDiff != run.Diff {
		return "", approvals.ErrPayloadChanged
	}
	payload := commitPayload{RunID: runID, Branch: run.Branch, BaseRevision: run.BaseRevision, Diff: run.Diff, Message: message}
	if err := s.policy.Authorize(ctx, approvals.Commit, payload, approvalID); err != nil {
		return "", err
	}
	commit, err := s.committer.Commit(ctx, run.Workspace, message)
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
	if !s.capabilities.Push || s.pusher == nil {
		return ErrRepositoryPushUnavailable
	}
	run, _, err := s.run(ctx, runID)
	if err != nil {
		return err
	}
	payload := pushPayload{RunID: runID, Branch: branch, Commit: run.Commit}
	head, err := s.pusher.Head(ctx, run.Workspace)
	if err != nil {
		return err
	}
	if run.Commit == "" || head != run.Commit {
		return approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.Push, payload, approvalID); err != nil {
		return err
	}
	return s.pusher.Push(ctx, run.Workspace, branch)
}

func (s *ShippingService) CreatePullRequest(ctx context.Context, runID, branch, title, body, approvalID string) (ports.PullRequest, error) {
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return ports.PullRequest{}, ErrPullRequestUnavailable
	}
	run, repository, err := s.run(ctx, runID)
	if err != nil {
		return ports.PullRequest{}, err
	}
	payload := pullRequestPayload{RunID: runID, Branch: branch, Commit: run.Commit, Title: title, Body: body}
	remoteHead, err := s.pullRequests.RemoteHead(ctx, run.Workspace, branch)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if run.Commit == "" || remoteHead != run.Commit {
		return ports.PullRequest{}, approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.CreatePR, payload, approvalID); err != nil {
		return ports.PullRequest{}, err
	}
	return s.pullRequests.CreatePullRequest(ctx, repository, branch, title, body)
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
	repository, ok := state.Repositories[run.Repository]
	if !ok {
		return ports.CodingRun{}, ports.Repository{}, errors.New("repository is not registered")
	}
	return run, repository, nil
}
