package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestShippingRequiresIndependentExactApprovals(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {ID: "run-1", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", Diff: "diff"}}
	policy := &fakePolicy{}
	repository := &fakeRepository{}
	service := NewShippingService(store, policy, repository, map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}}})

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
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "feature", Diff: "diff"}}
	policy := &fakePolicy{err: approvals.ErrPayloadChanged}
	repository := &fakeRepository{}
	service := NewShippingService(store, policy, repository, map[string]ports.Repository{"eggy": {Name: "eggy"}})
	if _, err := service.Commit(context.Background(), "run", "message", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("error=%v", err)
	}
	if repository.commits != 0 {
		t.Fatal("commit happened before authorization")
	}
}

func TestShippingInvalidatesCommitApprovalWhenWorkspaceDiffChanged(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Repository: "eggy", Workspace: "/tmp/run", Diff: "approved-diff"}}
	policy := &fakePolicy{}
	repository := &fakeRepository{diff: "changed-diff"}
	service := NewShippingService(store, policy, repository, map[string]ports.Repository{"eggy": {Name: "eggy"}})
	if _, err := service.Commit(context.Background(), "run", "message", "approval"); !errors.Is(err, approvals.ErrPayloadChanged) {
		t.Fatalf("error=%v", err)
	}
	if len(policy.actions) != 0 || repository.commits != 0 {
		t.Fatalf("authorization/commit occurred: %#v %#v", policy, repository)
	}
}

func TestShippingRejectsMovedLocalOrRemoteCommit(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", Commit: "approved"}}
	policy := &fakePolicy{}
	repository := &fakeRepository{head: "moved", remoteHead: "moved"}
	service := NewShippingService(store, policy, repository, map[string]ports.Repository{"eggy": {Name: "eggy"}})
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
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Repository: "eggy", Workspace: "/tmp/run", Branch: "eggy/run", Diff: "diff"}}
	gateway := &fakeShippingGateway{}
	repository := &fakeRepository{}
	service := NewShippingService(store, gateway, repository, map[string]ports.Repository{"eggy": {Name: "eggy"}})
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

type fakePolicy struct {
	actions  []approvals.Action
	payloads []any
	ids      []string
	err      error
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

func (p *fakePolicy) Authorize(_ context.Context, action approvals.Action, payload any, id string) error {
	p.actions = append(p.actions, action)
	p.payloads = append(p.payloads, payload)
	p.ids = append(p.ids, id)
	return p.err
}

type fakeRepository struct {
	clones, branches, commits, pushes, prs int
	diff, head, remoteHead                 string
	guidance                               string
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
func (r *fakeRepository) CreateBranch(context.Context, string, string) error {
	r.branches++
	return nil
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
