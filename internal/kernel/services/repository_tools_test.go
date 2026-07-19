package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryToolsListInspectAndModify(t *testing.T) {
	repositories := map[string]ports.Repository{
		"zeta": {Name: "zeta", BaseBranch: "trunk", ProtectedBranches: []string{"trunk"}},
		"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}},
	}
	worker := &fakeRepositoryWorker{}
	requester := &fakeCommitRequester{approval: approvals.Approval{ID: "approval-1", Action: approvals.Commit, Status: approvals.Pending}}
	var delivered approvals.Approval
	tools := NewRepositoryTools(repositories, worker, worker, requester, func() string { return "run-1" }, nil, func(_ context.Context, approval approvals.Approval) error {
		delivered = approval
		return nil
	})
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	listed, err := byName["repository_list"].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || strings.Index(string(listed), `"eggy"`) > strings.Index(string(listed), `"zeta"`) || strings.Contains(string(listed), "CloneURL") {
		t.Fatalf("listed=%s err=%v", listed, err)
	}
	inspected, err := byName["repository_inspect"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","question":"framework?"}`))
	if err != nil || !strings.Contains(string(inspected), "Go standard library") || worker.inspected != "eggy:framework?" {
		t.Fatalf("inspected=%s worker=%#v err=%v", inspected, worker, err)
	}
	modified, err := byName["repository_modify"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","instruction":"fix tests"}`))
	if err != nil || !strings.Contains(string(modified), `"status":"awaiting_owner"`) || requester.runID != "run-1" || delivered.ID != "approval-1" {
		t.Fatalf("modified=%s requester=%#v delivered=%#v err=%v", modified, requester, delivered, err)
	}
	if _, err := byName["repository_inspect"].Execute(context.Background(), json.RawMessage(`{"repository":"missing","question":"framework?"}`)); err == nil {
		t.Fatal("expected unknown repository error")
	}
}

func TestRepositoryModifyStampsRunIDOnProgressEvents(t *testing.T) {
	repositories := map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	worker := &fakeRepositoryWorker{}
	requester := &fakeCommitRequester{approval: approvals.Approval{ID: "approval-1"}}
	var received []ports.CodingProgress
	tools := NewRepositoryTools(repositories, worker, worker, requester, func() string { return "run-42" },
		func(progress ports.CodingProgress) { received = append(received, progress) },
		func(context.Context, approvals.Approval) error { return nil })
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	if _, err := byName["repository_modify"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","instruction":"fix tests"}`)); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].RunID != "run-42" || received[0].Message != "working" {
		t.Fatalf("received=%#v", received)
	}
}

func TestRepositoryListReportsNotConfigured(t *testing.T) {
	tools := NewRepositoryTools(nil, &fakeRepositoryWorker{}, &fakeRepositoryWorker{}, &fakeCommitRequester{}, func() string { return "run" }, nil, nil)
	result, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(result), `"status":"not_configured"`) {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

type fakeRepositoryWorker struct{ inspected string }

func (w *fakeRepositoryWorker) Inspect(_ context.Context, _ string, repository ports.Repository, question string) (ports.CodingResult, error) {
	w.inspected = repository.Name + ":" + question
	return ports.CodingResult{Summary: "Go standard library", Validation: "clean"}, nil
}

func (w *fakeRepositoryWorker) Start(_ context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "working"})
	}
	return ports.CodingRun{ID: runID, Repository: repository.Name}, ports.CodingResult{Summary: "fixed", Validation: "tests pass", CommitMessage: "fix: tests"}, nil
}

type fakeCommitRequester struct {
	approval approvals.Approval
	runID    string
	message  string
}

func (r *fakeCommitRequester) RequestCommit(_ context.Context, runID, message string) (approvals.Approval, error) {
	r.runID, r.message = runID, message
	return r.approval, nil
}
