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
	sessions     *ImplementationSessions
	policy       ports.ApprovalPolicy
	workspace    ports.WorkspaceInspector
	committer    ports.RepositoryCommitter
	pusher       ports.RepositoryPusher
	pullRequests ports.PullRequestProvider
	capabilities ports.RepositoryCapabilities
	requester    ApprovalRequester
	decider      ApprovalDecider
}

// ApprovalDecider immediately decides a pending approval, standing in for a
// human Telegram tap so Ship can run the commit/push/PR chain unattended.
type ApprovalDecider interface {
	Decide(ctx context.Context, id string, approved bool) error
}

var (
	ErrRepositoryCommitUnavailable = errors.New("repository commit capability is unavailable")
	ErrRepositoryPushUnavailable   = errors.New("repository push capability is unavailable")
	ErrPullRequestUnavailable      = errors.New("pull-request capability is unavailable")
)

func (s *ShippingService) SetApprovalRequester(requester ApprovalRequester) { s.requester = requester }

func (s *ShippingService) SetApprovalDecider(decider ApprovalDecider) { s.decider = decider }

// Ship runs commit, push, and pull-request creation back to back, deciding
// each step's approval itself instead of waiting for an owner Telegram tap.
// It returns the pull request (created, or an already-open one for the
// branch that was reused so Eggy keeps improving the same pull request
// instead of opening a new one every round), or a non-empty note describing
// where the chain stopped (an unavailable capability or a protected branch)
// with a nil error, since those are expected outcomes rather than failures.
func (s *ShippingService) Ship(ctx context.Context, runID, branch, commitMessage string) (ports.PullRequest, string, error) {
	if s.decider == nil {
		return ports.PullRequest{}, "", errors.New("automatic shipping approval is unavailable")
	}
	commitApproval, err := s.RequestCommit(ctx, runID, commitMessage)
	if err != nil {
		return ports.PullRequest{}, "", err
	}
	if err := s.decider.Decide(ctx, commitApproval.ID, true); err != nil {
		return ports.PullRequest{}, "", err
	}
	if _, err := s.ExecuteApproved(ctx, commitApproval); err != nil {
		return ports.PullRequest{}, "", err
	}

	pushApproval, err := s.RequestPush(ctx, runID, branch)
	if err != nil {
		if errors.Is(err, ErrRepositoryPushUnavailable) {
			return ports.PullRequest{}, "Committed. Push is unavailable for the configured repository provider.", nil
		}
		return ports.PullRequest{}, "", err
	}
	if err := s.decider.Decide(ctx, pushApproval.ID, true); err != nil {
		return ports.PullRequest{}, "", err
	}
	if _, err := s.ExecuteApproved(ctx, pushApproval); err != nil {
		if errors.Is(err, approvals.ErrProtectedBranch) {
			return ports.PullRequest{}, "Committed, but " + branch + " is a protected branch; push was denied.", nil
		}
		return ports.PullRequest{}, "", err
	}

	prApproval, err := s.RequestPullRequest(ctx, runID, branch, "Eggy: "+branch, "Automated by Eggy after a validated implementation run.")
	if err != nil {
		if errors.Is(err, ErrPullRequestUnavailable) {
			return ports.PullRequest{}, "Pushed. Pull-request creation is unavailable for the configured repository provider.", nil
		}
		return ports.PullRequest{}, "", err
	}
	if err := s.decider.Decide(ctx, prApproval.ID, true); err != nil {
		return ports.PullRequest{}, "", err
	}
	result, err := s.ExecuteApproved(ctx, prApproval)
	if err != nil {
		return ports.PullRequest{}, "", err
	}
	pr, ok := result.(ports.PullRequest)
	if !ok {
		return ports.PullRequest{}, "", errors.New("pull-request creation returned an unexpected result")
	}
	return pr, "", nil
}

func NewShippingService(store ports.StateStore, sessions *ImplementationSessions, policy ports.ApprovalPolicy, workspace ports.WorkspaceInspector, committer ports.RepositoryCommitter, pusher ports.RepositoryPusher, pullRequests ports.PullRequestProvider, capabilities ports.RepositoryCapabilities) *ShippingService {
	return &ShippingService{store: store, sessions: sessions, policy: policy, workspace: workspace, committer: committer, pusher: pusher, pullRequests: pullRequests, capabilities: capabilities}
}

func (s *ShippingService) RequestCommit(ctx context.Context, runID, message string) (approvals.Approval, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return approvals.Approval{}, ErrRepositoryCommitUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := commitPayload{RunID: runID, Branch: session.Branch, BaseRevision: session.BaseRevision, Diff: session.Diff, Message: message}
	return s.requester.Request(ctx, approvals.Commit, payload, "Commit changes for "+runID)
}

func (s *ShippingService) RequestPush(ctx context.Context, runID, branch string) (approvals.Approval, error) {
	if !s.capabilities.Push || s.pusher == nil {
		return approvals.Approval{}, ErrRepositoryPushUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := pushPayload{RunID: runID, Branch: branch, Commit: session.Commit}
	return s.requester.Request(ctx, approvals.Push, payload, "Push "+branch)
}

func (s *ShippingService) RequestPullRequest(ctx context.Context, runID, branch, title, body string) (approvals.Approval, error) {
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return approvals.Approval{}, ErrPullRequestUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return approvals.Approval{}, err
	}
	if s.requester == nil {
		return approvals.Approval{}, errors.New("approval requester is unavailable")
	}
	payload := pullRequestPayload{RunID: runID, Branch: branch, Commit: session.Commit, Title: title, Body: body}
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

type commitPayload struct{ RunID, Branch, BaseRevision, Diff, Message string }
type pushPayload struct{ RunID, Branch, Commit string }
type pullRequestPayload struct{ RunID, Branch, Commit, Title, Body string }

func (s *ShippingService) Commit(ctx context.Context, runID, message, approvalID string) (string, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return "", ErrRepositoryCommitUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return "", err
	}
	currentRevision, err := s.workspace.WorkspaceRevision(ctx, session.Workspace)
	if err != nil {
		return "", err
	}
	if session.Branch == "" || session.BaseRevision == "" || currentRevision.Branch != session.Branch || currentRevision.Head != session.BaseRevision {
		return "", approvals.ErrPayloadChanged
	}
	currentDiff, err := s.committer.Diff(ctx, session.Workspace)
	if err != nil {
		return "", err
	}
	if currentDiff != session.Diff {
		return "", approvals.ErrPayloadChanged
	}
	payload := commitPayload{RunID: runID, Branch: session.Branch, BaseRevision: session.BaseRevision, Diff: session.Diff, Message: message}
	if err := s.policy.Authorize(ctx, approvals.Commit, payload, approvalID); err != nil {
		return "", err
	}
	commit, err := s.committer.Commit(ctx, session.Workspace, message)
	if err != nil {
		return "", err
	}
	if err := s.sessions.RecordCommit(ctx, runID, commit); err != nil {
		return commit, err
	}
	if err := s.sessions.SetPhase(ctx, runID, ports.PhaseCommitted, "Commit created"); err != nil {
		return commit, err
	}
	return commit, nil
}

func (s *ShippingService) Push(ctx context.Context, runID, branch, approvalID string) error {
	if !s.capabilities.Push || s.pusher == nil {
		return ErrRepositoryPushUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return err
	}
	payload := pushPayload{RunID: runID, Branch: branch, Commit: session.Commit}
	head, err := s.pusher.Head(ctx, session.Workspace)
	if err != nil {
		return err
	}
	if session.Commit == "" || head != session.Commit {
		return approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.Push, payload, approvalID); err != nil {
		return err
	}
	if err := s.pusher.Push(ctx, session.Workspace, branch); err != nil {
		return err
	}
	return s.sessions.SetPhase(ctx, runID, ports.PhasePushed, "Branch pushed")
}

func (s *ShippingService) CreatePullRequest(ctx context.Context, runID, branch, title, body, approvalID string) (ports.PullRequest, error) {
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return ports.PullRequest{}, ErrPullRequestUnavailable
	}
	session, err := s.session(ctx, runID)
	if err != nil {
		return ports.PullRequest{}, err
	}
	repository, err := s.repositoryFor(ctx, session)
	if err != nil {
		return ports.PullRequest{}, err
	}
	payload := pullRequestPayload{RunID: runID, Branch: branch, Commit: session.Commit, Title: title, Body: body}
	remoteHead, err := s.pullRequests.RemoteHead(ctx, session.Workspace, branch)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if session.Commit == "" || remoteHead != session.Commit {
		return ports.PullRequest{}, approvals.ErrPayloadChanged
	}
	if err := s.policy.Authorize(ctx, approvals.CreatePR, payload, approvalID); err != nil {
		return ports.PullRequest{}, err
	}
	if existing, found, err := s.pullRequests.FindOpenPullRequest(ctx, repository, branch); err != nil {
		return ports.PullRequest{}, err
	} else if found {
		// Reuse the already-open pull request instead of opening a
		// duplicate: the new commits just pushed already show up on it
		// automatically, so this is Eggy continuing to improve the same
		// pull request rather than starting a new one.
		_ = s.pullRequests.UpdatePullRequestBody(ctx, repository, existing.Number, "Updated by Eggy after a new implementation round.")
		if err := s.recordPullRequest(ctx, runID, existing); err != nil {
			return existing, err
		}
		return existing, nil
	}
	result, err := s.pullRequests.CreatePullRequest(ctx, repository, branch, title, body)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if err := s.recordPullRequest(ctx, runID, result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *ShippingService) recordPullRequest(ctx context.Context, runID string, pr ports.PullRequest) error {
	if err := s.sessions.RecordPullRequest(ctx, runID, pr.URL, pr.Number); err != nil {
		return err
	}
	return s.sessions.SetPhase(ctx, runID, ports.PhaseCompleted, "Pull request created")
}

func (s *ShippingService) session(ctx context.Context, id string) (ports.ImplementationSession, error) {
	if s.sessions == nil {
		return ports.ImplementationSession{}, errors.New("implementation sessions are unavailable")
	}
	return s.sessions.Load(ctx, id)
}

func (s *ShippingService) repositoryFor(ctx context.Context, session ports.ImplementationSession) (ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.Repository{}, err
	}
	repository, ok := state.Repositories[session.Repository]
	if !ok {
		return ports.Repository{}, errors.New("repository is not registered")
	}
	return repository, nil
}
