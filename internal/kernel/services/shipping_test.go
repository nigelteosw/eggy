package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

var fullRepositoryCapabilities = ports.RepositoryCapabilities{Commit: true, Push: true, PullRequest: true}

// shippingFixture builds a real ImplementationSessions instance seeded with
// session, so shipping tests exercise the same canonical store production
// code uses instead of a lifecycle fake.
func shippingFixture(session ports.ImplementationSession) (*ImplementationSessions, *memorySessionStore) {
	store := newMemorySessionStore()
	store.sessions[session.ID] = session
	return NewImplementationSessions(store, SessionPolicy{}, time.Now), store
}

func TestShippingRequiresIndependentExactApprovals(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", BaseRevision: "abc123", Diff: "diff"})
	policy := &fakePolicy{}
	repository := &fakeRepository{branch: "feature"}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}}}
	service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)

	commit, err := service.Commit(context.Background(), "run-1", "feat: done", "approval-commit")
	if err != nil || commit != "abc123" {
		t.Fatalf("commit=%q err=%v", commit, err)
	}
	if policy.actions[0] != approvals.Commit || repository.commits != 1 {
		t.Fatalf("policy=%#v repository=%#v", policy.actions, repository)
	}
	if err := service.Push(context.Background(), "run-1", "feature", "approval-push"); err != nil {
		t.Fatal(err)
	}
	pr, err := service.CreatePullRequest(context.Background(), "run-1", "feature", "Title", "Body", "approval-pr")
	if err != nil || pr.Number != 1 {
		t.Fatalf("pr=%#v err=%v", pr, err)
	}
	if len(policy.actions) != 3 || policy.actions[1] != approvals.Push || policy.actions[2] != approvals.CreatePR {
		t.Fatalf("actions=%#v", policy.actions)
	}
}

func TestShippingStopsBeforeSideEffectWhenApprovalFails(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", BaseRevision: "abc123", Diff: "diff"})
	policy := &fakePolicy{err: approvals.ErrPayloadChanged}
	repository := &fakeRepository{branch: "feature"}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)
	if _, err := service.Commit(context.Background(), "run", "message", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("error=%v", err)
	}
	if repository.commits != 0 {
		t.Fatal("commit happened before authorization")
	}
}

func TestShippingInvalidatesCommitApprovalWhenWorkspaceDiffChanged(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", BaseRevision: "abc123", Diff: "approved-diff"})
	policy := &fakePolicy{}
	repository := &fakeRepository{branch: "feature", diff: "changed-diff"}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)
	if _, err := service.Commit(context.Background(), "run", "message", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("error=%v", err)
	}
	if len(policy.actions) != 0 || repository.commits != 0 {
		t.Fatalf("authorization/commit occurred: %#v %#v", policy, repository)
	}
}

func TestShippingInvalidatesCommitApprovalWhenWorkspaceRevisionChanged(t *testing.T) {
	for _, test := range []struct {
		name, branch, head string
	}{
		{name: "branch", branch: "other", head: "abc123"},
		{name: "head", branch: "feature", head: "moved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", BaseRevision: "abc123", Diff: "diff"})
			store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
			policy := &fakePolicy{}
			repository := &fakeRepository{branch: test.branch, head: test.head}
			service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)

			if _, err := service.Commit(context.Background(), "run", "message", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
				t.Fatalf("error=%v", err)
			}
			if len(policy.actions) != 0 || repository.commits != 0 {
				t.Fatalf("authorization/commit occurred: %#v %#v", policy, repository)
			}
		})
	}
}

func TestShippingRejectsMovedLocalOrRemoteCommit(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", Commit: "approved"})
	policy := &fakePolicy{}
	repository := &fakeRepository{head: "moved", remoteHead: "moved"}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)
	if err := service.Push(context.Background(), "run", "eggy/run", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("push error=%v", err)
	}
	repository.head = "approved"
	if _, err := service.CreatePullRequest(context.Background(), "run", "eggy/run", "Title", "Body", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("PR error=%v", err)
	}
	if len(policy.actions) != 0 || repository.pushes != 0 || repository.prs != 0 {
		t.Fatalf("side effect reached: %#v %#v", policy, repository)
	}
}

func TestShippingPersistsAndResumesApprovedAction(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", BaseRevision: "abc123", Diff: "diff"})
	gateway := &fakeShippingGateway{}
	repository := &fakeRepository{branch: "eggy/run"}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	service := NewShippingService(store, sessions, gateway, repository, repository, repository, repository, fullRepositoryCapabilities)
	service.SetApprovalRequester(gateway)
	approval, err := service.RequestCommit(context.Background(), "run", "feat: done")
	if err != nil {
		t.Fatal(err)
	}
	approval.Payload, _ = json.Marshal(gateway.payload)
	if _, err := service.ExecuteApproved(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if repository.commits != 1 || gateway.authorized != approvals.Commit {
		t.Fatalf("repository=%#v gateway=%#v", repository, gateway)
	}
}

// TestShippingRecordsDurableSessionLifecycle proves the full commit -> push
// -> pull-request chain records each milestone, in order, on the canonical
// session -- and ends at PhaseCompleted. There is no separate
// awaiting-approval phase between these steps: Ship decides every approval
// automatically, so those states from the old two-store design would only
// ever have been instantaneous.
func TestShippingRecordsDurableSessionLifecycle(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", BaseRevision: "abc123", Diff: "diff"})
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	repository := &fakeRepository{branch: "eggy/run"}
	service := NewShippingService(store, sessions, &fakePolicy{}, repository, repository, repository, repository, fullRepositoryCapabilities)
	service.SetApprovalRequester(&fakeShippingGateway{})

	if _, err := service.Commit(context.Background(), "run", "feat: done", "commit-approval"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestPush(context.Background(), "run", "eggy/run"); err != nil {
		t.Fatal(err)
	}
	if err := service.Push(context.Background(), "run", "eggy/run", "push-approval"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestPullRequest(context.Background(), "run", "eggy/run", "title", "body"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePullRequest(context.Background(), "run", "eggy/run", "title", "body", "pr-approval"); err != nil {
		t.Fatal(err)
	}
	session, err := sessions.Load(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != ports.PhaseCompleted || session.Commit != "abc123" || session.PullRequestURL != "https://example/pr/1" {
		t.Fatalf("session=%#v", session)
	}
	var milestones []string
	for _, event := range session.Events {
		if event.Kind == ports.SessionMilestone {
			milestones = append(milestones, event.Message)
		}
	}
	want := []string{"Commit created", "Branch pushed", "Pull request created"}
	if len(milestones) != len(want) {
		t.Fatalf("milestones=%#v", milestones)
	}
	for i, message := range want {
		if milestones[i] != message {
			t.Fatalf("milestones=%#v want=%#v", milestones, want)
		}
	}
}

func TestShippingRejectsUnavailablePushAndPullRequestCapabilities(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", Commit: "abc123"})
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	gateway := &fakeShippingGateway{}
	repository := &fakeRepository{}
	service := NewShippingService(store, sessions, gateway, repository, repository, repository, repository, ports.RepositoryCapabilities{Commit: true})
	service.SetApprovalRequester(gateway)

	if _, err := service.RequestPush(context.Background(), "run", "eggy/run"); !errors.Is(err, ErrRepositoryPushUnavailable) {
		t.Fatalf("push request error=%v", err)
	}
	if _, err := service.RequestPullRequest(context.Background(), "run", "eggy/run", "title", "body"); !errors.Is(err, ErrPullRequestUnavailable) {
		t.Fatalf("pull request error=%v", err)
	}
	if err := service.Push(context.Background(), "run", "eggy/run", "approval"); !errors.Is(err, ErrRepositoryPushUnavailable) {
		t.Fatalf("push error=%v", err)
	}
	if _, err := service.CreatePullRequest(context.Background(), "run", "eggy/run", "title", "body", "approval"); !errors.Is(err, ErrPullRequestUnavailable) {
		t.Fatalf("create pull request error=%v", err)
	}
	if repository.pushes != 0 || repository.prs != 0 {
		t.Fatalf("unavailable side effect reached repository=%#v", repository)
	}
}

// TestShippingReusesExistingOpenPullRequestInsteadOfCreatingDuplicate is the
// "keep editing the same MR" requirement: if a branch already has an open
// pull request (e.g. a previous /continue round already shipped one),
// CreatePullRequest must reuse it -- report its URL/number and record it on
// the session -- rather than attempting to open a second one.
func TestShippingReusesExistingOpenPullRequestInsteadOfCreatingDuplicate(t *testing.T) {
	store := newMemoryStore()
	sessions, _ := shippingFixture(ports.ImplementationSession{ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", Commit: "abc123"})
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	repository := &fakeRepository{existingPR: &ports.PullRequest{Number: 7, URL: "https://example/pr/7"}}
	policy := &fakePolicy{}
	service := NewShippingService(store, sessions, policy, repository, repository, repository, repository, fullRepositoryCapabilities)

	pr, err := service.CreatePullRequest(context.Background(), "run", "eggy/run", "title", "body", "approval")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.URL != "https://example/pr/7" {
		t.Fatalf("pr=%#v, want the existing open pull request reused", pr)
	}
	if repository.prs != 0 {
		t.Fatalf("prs created=%d, want 0 (must reuse, never duplicate)", repository.prs)
	}
	if repository.updatedPRNumber != 7 {
		t.Fatalf("updatedPRNumber=%d, want the existing PR edited to reflect this round", repository.updatedPRNumber)
	}
	session, err := sessions.Load(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if session.PullRequestNumber != 7 || session.PullRequestURL != "https://example/pr/7" || session.Phase != ports.PhaseCompleted {
		t.Fatalf("session=%#v", session)
	}
}

type fakePolicy struct {
	actions  []approvals.Action
	payloads []any
	ids      []string
	err      error
}

func (p *fakePolicy) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	p.actions = append(p.actions, action)
	p.payloads = append(p.payloads, payload)
	p.ids = append(p.ids, id)
	return p.err
}

type fakeShippingGateway struct {
	payload    any
	authorized approvals.Action
}

func (g *fakeShippingGateway) Request(_ context.Context, action approvals.Action, payload any, summary string) (approvals.Approval, error) {
	g.payload = payload
	return approvals.Approval{ID: "approval", Action: action, Summary: summary}, nil
}
func (g *fakeShippingGateway) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	g.authorized = action
	return nil
}

type fakeRepository struct {
	clones, branches, commits, pushes, prs int
	diff, head, remoteHead, branch         string
	guidance                               string
	existingPR                             *ports.PullRequest
	updatedPRNumber                        int
}

func (r *fakeRepository) Clone(context.Context, ports.Repository, string) error {
	r.clones++
	return nil
}
func (r *fakeRepository) Inspect(context.Context, string) (string, error) {
	if r.guidance != "" {
		return r.guidance, nil
	}
	return "Follow AGENTS.md", nil
}
func (r *fakeRepository) CreateBranch(_ context.Context, _, branch string) error {
	r.branches++
	r.branch = branch
	return nil
}
func (r *fakeRepository) WorkspaceRevision(context.Context, string) (ports.WorkspaceRevision, error) {
	head := r.head
	if head == "" {
		head = "abc123"
	}
	branch := r.branch
	if branch == "" {
		branch = "main"
	}
	return ports.WorkspaceRevision{Branch: branch, Head: head}, nil
}
func (r *fakeRepository) Head(context.Context, string) (string, error) {
	if r.head != "" {
		return r.head, nil
	}
	return "abc123", nil
}
func (r *fakeRepository) RemoteHead(context.Context, string, string) (string, error) {
	if r.remoteHead != "" {
		return r.remoteHead, nil
	}
	return "abc123", nil
}
func (r *fakeRepository) Diff(context.Context, string) (string, error) {
	if r.diff != "" {
		return r.diff, nil
	}
	return "diff", nil
}
func (r *fakeRepository) Commit(context.Context, string, string) (string, error) {
	r.commits++
	return "abc123", nil
}
func (r *fakeRepository) Push(context.Context, string, string) error { r.pushes++; return nil }
func (r *fakeRepository) CreatePullRequest(context.Context, ports.Repository, string, string, string) (ports.PullRequest, error) {
	r.prs++
	return ports.PullRequest{Number: 1, URL: "https://example/pr/1"}, nil
}
func (r *fakeRepository) FindOpenPullRequest(context.Context, ports.Repository, string) (ports.PullRequest, bool, error) {
	if r.existingPR != nil {
		return *r.existingPR, true, nil
	}
	return ports.PullRequest{}, false, nil
}
func (r *fakeRepository) UpdatePullRequestBody(_ context.Context, _ ports.Repository, number int, _ string) error {
	r.updatedPRNumber = number
	return nil
}
