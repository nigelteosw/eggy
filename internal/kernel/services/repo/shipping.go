package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nigelteosw/eggy/internal/kernel/services"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ShipTarget is what shipping needs that a Change deliberately does not
// store: the live checkout the change is being shipped from, which belongs
// to the thread, and the transcript its milestones are recorded on.
type ShipTarget struct {
	ChangeID   string
	Workspace  string
	Transcript string
	// Draft marks this as an unprompted turn's proposal: the pull request is
	// opened as a draft for the owner to review. It travels inside every
	// shipping payload, so it is covered by the same payload-digest
	// authorization as the branch and diff rather than being a hint the
	// executing step could quietly drop.
	Draft bool
}

type ShippingService struct {
	store        ports.StateStore
	changes      *Changes
	transcripts  *services.Transcripts
	authorizer   ShippingAuthorizer
	workspace    ports.WorkspaceInspector
	committer    ports.RepositoryCommitter
	pusher       ports.RepositoryPusher
	pullRequests ports.PullRequestProvider
	capabilities ports.RepositoryCapabilities
}

// ShippingAuthorizer is the single authorization boundary Ship uses to issue
// and immediately decide, then later authorize, each commit/push/pull-request
// approval unattended -- standing in for a human Telegram tap. RequestAndApprove
// and Authorize are still two separate calls (issue-and-decide, then
// consume-on-use) so Commit/Push/CreatePullRequest keep re-checking the
// workspace/diff/head state that changed between them.
type ShippingAuthorizer interface {
	RequestAndApprove(ctx context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error)
	Authorize(ctx context.Context, action approvals.Action, payload any, approvalID string) error
}

var (
	ErrRepositoryCommitUnavailable = errors.New("repository commit capability is unavailable")
	ErrRepositoryPushUnavailable   = errors.New("repository push capability is unavailable")
	ErrPullRequestUnavailable      = errors.New("pull-request capability is unavailable")
	// ErrUnpromptedBaseBranch is returned when an unprompted turn's proposal
	// would target the repository's base branch or one of its protected
	// branches. An unprompted turn only ever proposes on a branch of its own.
	ErrUnpromptedBaseBranch = errors.New("an unprompted turn cannot propose a change on a base or protected branch")
)

// rejectBaseBranch fails a draft proposal that would land on the repository's
// own base or protected branch.
func (s *ShippingService) rejectBaseBranch(ctx context.Context, changeID, branch string) error {
	change, err := s.change(ctx, changeID)
	if err != nil {
		return err
	}
	repository, err := s.repositoryFor(ctx, change)
	if err != nil {
		return err
	}
	if branch == repository.BaseBranch {
		return fmt.Errorf("%w: %s", ErrUnpromptedBaseBranch, branch)
	}
	for _, protected := range repository.ProtectedBranches {
		if branch == protected {
			return fmt.Errorf("%w: %s", ErrUnpromptedBaseBranch, branch)
		}
	}
	return nil
}

// Ship runs commit, push, and pull-request creation back to back, deciding
// each step's approval itself instead of waiting for an owner Telegram tap.
// It returns the pull request (created, or an already-open one for the
// branch that was reused so Eggy keeps improving the same pull request
// instead of opening a new one every round), or a non-empty note describing
// where the chain stopped (an unavailable capability or a protected branch)
// with a nil error, since those are expected outcomes rather than failures.
func (s *ShippingService) Ship(ctx context.Context, target ShipTarget, branch, commitMessage string) (ports.PullRequest, string, error) {
	if target.Draft {
		// An unprompted turn proposes on an isolated branch of its own and
		// nowhere else. Push already refuses a protected branch, but a base
		// branch that nobody listed as protected would slip through, and this
		// invariant must not depend on a repository's configuration being
		// thorough.
		if err := s.rejectBaseBranch(ctx, target.ChangeID, branch); err != nil {
			return ports.PullRequest{}, "", err
		}
	}
	commitApproval, err := s.RequestCommit(ctx, target, commitMessage)
	if err != nil {
		return ports.PullRequest{}, "", err
	}
	if _, err := s.ExecuteApproved(ctx, commitApproval); err != nil {
		return ports.PullRequest{}, "", err
	}

	pushApproval, err := s.RequestPush(ctx, target, branch)
	if err != nil {
		if errors.Is(err, ErrRepositoryPushUnavailable) {
			return ports.PullRequest{}, "Committed. Push is unavailable for the configured repository provider.", nil
		}
		return ports.PullRequest{}, "", err
	}
	if _, err := s.ExecuteApproved(ctx, pushApproval); err != nil {
		if errors.Is(err, approvals.ErrProtectedBranch) {
			return ports.PullRequest{}, "Committed, but " + branch + " is a protected branch; push was denied.", nil
		}
		return ports.PullRequest{}, "", err
	}

	body := "Automated by Eggy after a validated implementation run."
	if target.Draft {
		body = "Proposed by Eggy from an unprompted turn (scheduled or heartbeat) after a validated implementation run. Opened as a draft: nothing here is claimed as finished work, and it lands only if you review and merge it."
	}
	prApproval, err := s.RequestPullRequest(ctx, target, branch, "Eggy: "+branch, body)
	if err != nil {
		if errors.Is(err, ErrPullRequestUnavailable) {
			return ports.PullRequest{}, "Pushed. Pull-request creation is unavailable for the configured repository provider.", nil
		}
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

func NewShippingService(store ports.StateStore, changes *Changes, transcripts *services.Transcripts, authorizer ShippingAuthorizer, workspace ports.WorkspaceInspector, committer ports.RepositoryCommitter, pusher ports.RepositoryPusher, pullRequests ports.PullRequestProvider, capabilities ports.RepositoryCapabilities) *ShippingService {
	return &ShippingService{store: store, changes: changes, transcripts: transcripts, authorizer: authorizer, workspace: workspace, committer: committer, pusher: pusher, pullRequests: pullRequests, capabilities: capabilities}
}

func (s *ShippingService) RequestCommit(ctx context.Context, target ShipTarget, message string) (approvals.Approval, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return approvals.Approval{}, ErrRepositoryCommitUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return approvals.Approval{}, err
	}
	payload := commitPayload{Target: target, Branch: change.Branch, BaseRevision: change.BaseRevision, Diff: change.Diff, Message: message}
	return s.authorizer.RequestAndApprove(ctx, approvals.Commit, payload, "Commit changes for "+target.ChangeID)
}

func (s *ShippingService) RequestPush(ctx context.Context, target ShipTarget, branch string) (approvals.Approval, error) {
	if !s.capabilities.Push || s.pusher == nil {
		return approvals.Approval{}, ErrRepositoryPushUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return approvals.Approval{}, err
	}
	payload := pushPayload{Target: target, Branch: branch, Commit: change.Commit}
	return s.authorizer.RequestAndApprove(ctx, approvals.Push, payload, "Push "+branch)
}

func (s *ShippingService) RequestPullRequest(ctx context.Context, target ShipTarget, branch, title, body string) (approvals.Approval, error) {
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return approvals.Approval{}, ErrPullRequestUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return approvals.Approval{}, err
	}
	payload := pullRequestPayload{Target: target, Branch: branch, Commit: change.Commit, Title: title, Body: body}
	return s.authorizer.RequestAndApprove(ctx, approvals.CreatePR, payload, "Create pull request for "+branch)
}

func (s *ShippingService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	switch approval.Action {
	case approvals.Commit:
		var payload commitPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return s.Commit(ctx, payload.Target, payload.Message, approval.ID)
	case approvals.Push:
		var payload pushPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return nil, s.Push(ctx, payload.Target, payload.Branch, approval.ID)
	case approvals.CreatePR:
		var payload pullRequestPayload
		if err := json.Unmarshal(approval.Payload, &payload); err != nil {
			return nil, err
		}
		return s.CreatePullRequest(ctx, payload.Target, payload.Branch, payload.Title, payload.Body, approval.ID)
	default:
		return nil, errors.New("approval is not a shipping action")
	}
}

type commitPayload struct {
	Target                              ShipTarget
	Branch, BaseRevision, Diff, Message string
}
type pushPayload struct {
	Target         ShipTarget
	Branch, Commit string
}
type pullRequestPayload struct {
	Target                      ShipTarget
	Branch, Commit, Title, Body string
}

func (s *ShippingService) Commit(ctx context.Context, target ShipTarget, message, approvalID string) (string, error) {
	if !s.capabilities.Commit || s.workspace == nil || s.committer == nil {
		return "", ErrRepositoryCommitUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return "", err
	}
	currentRevision, err := s.workspace.WorkspaceRevision(ctx, target.Workspace)
	if err != nil {
		return "", err
	}
	if change.Branch == "" || change.BaseRevision == "" || currentRevision.Branch != change.Branch || currentRevision.Head != change.BaseRevision {
		return "", approvals.ErrPayloadChanged
	}
	currentDiff, err := s.committer.Diff(ctx, target.Workspace)
	if err != nil {
		return "", err
	}
	if currentDiff != change.Diff {
		return "", approvals.ErrPayloadChanged
	}
	payload := commitPayload{Target: target, Branch: change.Branch, BaseRevision: change.BaseRevision, Diff: change.Diff, Message: message}
	if err := s.authorizer.Authorize(ctx, approvals.Commit, payload, approvalID); err != nil {
		return "", err
	}
	commit, err := s.committer.Commit(ctx, target.Workspace, message)
	if err != nil {
		return "", err
	}
	if err := s.changes.RecordCommit(ctx, target.ChangeID, commit); err != nil {
		return commit, err
	}
	if err := s.transcripts.Milestone(ctx, target.Transcript, "Commit created"); err != nil {
		return commit, err
	}
	return commit, nil
}

func (s *ShippingService) Push(ctx context.Context, target ShipTarget, branch, approvalID string) error {
	if !s.capabilities.Push || s.pusher == nil {
		return ErrRepositoryPushUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return err
	}
	payload := pushPayload{Target: target, Branch: branch, Commit: change.Commit}
	head, err := s.pusher.Head(ctx, target.Workspace)
	if err != nil {
		return err
	}
	if change.Commit == "" || head != change.Commit {
		return approvals.ErrPayloadChanged
	}
	if err := s.authorizer.Authorize(ctx, approvals.Push, payload, approvalID); err != nil {
		return err
	}
	if err := s.pusher.Push(ctx, target.Workspace, branch); err != nil {
		return err
	}
	return s.transcripts.Milestone(ctx, target.Transcript, "Branch pushed")
}

func (s *ShippingService) CreatePullRequest(ctx context.Context, target ShipTarget, branch, title, body, approvalID string) (ports.PullRequest, error) {
	if !s.capabilities.PullRequest || s.pullRequests == nil {
		return ports.PullRequest{}, ErrPullRequestUnavailable
	}
	change, err := s.change(ctx, target.ChangeID)
	if err != nil {
		return ports.PullRequest{}, err
	}
	repository, err := s.repositoryFor(ctx, change)
	if err != nil {
		return ports.PullRequest{}, err
	}
	payload := pullRequestPayload{Target: target, Branch: branch, Commit: change.Commit, Title: title, Body: body}
	remoteHead, err := s.pullRequests.RemoteHead(ctx, target.Workspace, branch)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if change.Commit == "" || remoteHead != change.Commit {
		return ports.PullRequest{}, approvals.ErrPayloadChanged
	}
	if err := s.authorizer.Authorize(ctx, approvals.CreatePR, payload, approvalID); err != nil {
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
		if err := s.recordPullRequest(ctx, target, existing); err != nil {
			return existing, err
		}
		return existing, nil
	}
	result, err := s.pullRequests.CreatePullRequest(ctx, repository, branch, title, body, target.Draft)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if err := s.recordPullRequest(ctx, target, result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *ShippingService) recordPullRequest(ctx context.Context, target ShipTarget, pr ports.PullRequest) error {
	if err := s.changes.RecordPullRequest(ctx, target.ChangeID, pr.URL, pr.Number); err != nil {
		return err
	}
	if err := s.transcripts.Milestone(ctx, target.Transcript, "Pull request created"); err != nil {
		return err
	}
	return s.changes.SetPhase(ctx, target.ChangeID, ports.PhaseCompleted)
}

func (s *ShippingService) change(ctx context.Context, id string) (ports.Change, error) {
	if s.changes == nil {
		return ports.Change{}, errors.New("changes are unavailable")
	}
	return s.changes.Load(ctx, id)
}

func (s *ShippingService) repositoryFor(ctx context.Context, change ports.Change) (ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.Repository{}, err
	}
	repository, ok := state.Repositories[change.Repository]
	if !ok {
		return ports.Repository{}, errors.New("repository is not registered")
	}
	return repository, nil
}
