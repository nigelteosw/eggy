package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	repositoryNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	repositoryBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

type RepositoriesService struct {
	store        ports.StateStore
	runner       ports.Runner
	checker      ports.RemoteChecker
	requester    ApprovalRequester
	policy       ports.ApprovalPolicy
	capabilities ports.RepositoryCapabilities
	newRunID     func() string
}

func NewRepositoriesService(store ports.StateStore, runner ports.Runner, checker ports.RemoteChecker, requester ApprovalRequester, policy ports.ApprovalPolicy, capabilities ports.RepositoryCapabilities, newRunID func() string) *RepositoriesService {
	return &RepositoriesService{store: store, runner: runner, checker: checker, requester: requester, policy: policy, capabilities: capabilities, newRunID: newRunID}
}

type addRepositoryPayload struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

func (s *RepositoriesService) List(ctx context.Context) (map[string]ports.Repository, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Repositories, nil
}

func (s *RepositoriesService) Get(ctx context.Context, name string) (ports.Repository, bool, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return ports.Repository{}, false, err
	}
	repository, ok := state.Repositories[name]
	return repository, ok, nil
}

func (s *RepositoriesService) RequestAdd(ctx context.Context, name, cloneURL, baseBranch string, protectedBranches []string) (approvals.Approval, error) {
	if !s.capabilities.Commit || !s.capabilities.Push || !s.capabilities.PullRequest {
		return approvals.Approval{}, errors.New("repository provider must be ready for commit, push and pull-request creation")
	}
	if !repositoryNamePattern.MatchString(name) {
		return approvals.Approval{}, errors.New("repository name is invalid")
	}
	if cloneURL == "" {
		return approvals.Approval{}, errors.New("clone_url is required")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if !repositoryBranchPattern.MatchString(baseBranch) {
		return approvals.Approval{}, fmt.Errorf("base branch %q is invalid", baseBranch)
	}
	if len(protectedBranches) == 0 {
		protectedBranches = []string{baseBranch}
	}
	for _, branch := range protectedBranches {
		if !repositoryBranchPattern.MatchString(branch) {
			return approvals.Approval{}, fmt.Errorf("protected branch %q is invalid", branch)
		}
	}
	state, err := s.store.Load(ctx)
	if err != nil {
		return approvals.Approval{}, err
	}
	if _, exists := state.Repositories[name]; exists {
		return approvals.Approval{}, fmt.Errorf("repository %q already exists", name)
	}
	repository := ports.Repository{Name: name, CloneURL: cloneURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}
	workspace, err := s.runner.Create(ctx, s.newRunID())
	if err != nil {
		return approvals.Approval{}, err
	}
	defer s.runner.Destroy(context.Background(), workspace)
	if err := s.checker.CheckRemote(ctx, repository, workspace); err != nil {
		return approvals.Approval{}, err
	}
	payload := addRepositoryPayload{Name: name, CloneURL: cloneURL, BaseBranch: baseBranch, ProtectedBranches: protectedBranches}
	return s.requester.Request(ctx, approvals.AddRepository, payload, "Add repository "+name)
}

func (s *RepositoriesService) ExecuteApproved(ctx context.Context, approval approvals.Approval) (any, error) {
	if approval.Action != approvals.AddRepository {
		return nil, errors.New("approval is not a repositories action")
	}
	var payload addRepositoryPayload
	if err := json.Unmarshal(approval.Payload, &payload); err != nil {
		return nil, err
	}
	if err := s.policy.Authorize(ctx, approvals.AddRepository, payload, approval.ID); err != nil {
		return nil, err
	}
	repository := ports.Repository{Name: payload.Name, CloneURL: payload.CloneURL, BaseBranch: payload.BaseBranch, ProtectedBranches: payload.ProtectedBranches}
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.Repositories == nil {
			state.Repositories = map[string]ports.Repository{}
		}
		if _, exists := state.Repositories[repository.Name]; exists {
			return fmt.Errorf("repository %q already exists", repository.Name)
		}
		state.Repositories[repository.Name] = repository
		return nil
	})
	return repository, err
}

func (s *RepositoriesService) Remove(ctx context.Context, name string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	if _, ok := state.Repositories[name]; !ok {
		return fmt.Errorf("repository %q is not configured", name)
	}
	for _, run := range state.CodingRuns {
		if run.Repository == name && run.Status == "running" {
			return fmt.Errorf("repository %q has an active coding run %q", name, run.ID)
		}
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if _, ok := state.Repositories[name]; !ok {
			return fmt.Errorf("repository %q is not configured", name)
		}
		delete(state.Repositories, name)
		return nil
	})
	return err
}
