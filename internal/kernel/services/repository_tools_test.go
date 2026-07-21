package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryToolsListInspectAndModify(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{
		"zeta": {Name: "zeta", BaseBranch: "trunk", ProtectedBranches: []string{"trunk"}},
		"eggy": {Name: "eggy", BaseBranch: "main", ProtectedBranches: []string{"main"}},
	}
	worker := &fakeRepositoryWorker{}
	shipper := &fakeShipper{pr: ports.PullRequest{URL: "https://example.com/pr/1", Number: 1}}
	tools := NewRepositoryTools(store, worker, shipper, func() string { return "run-1" }, nil)
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	listed, err := byName["repository_list"].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || strings.Index(string(listed), `"eggy"`) > strings.Index(string(listed), `"zeta"`) || strings.Contains(string(listed), "CloneURL") {
		t.Fatalf("listed=%s err=%v", listed, err)
	}
	modified, err := byName["repository_modify"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","instruction":"fix tests"}`))
	var modifyResult map[string]any
	_ = json.Unmarshal(modified, &modifyResult)
	if err != nil || modifyResult["status"] != "shipped" || modifyResult["branch"] != "eggy/run-1" || modifyResult["pull_request_url"] != "https://example.com/pr/1" || modifyResult["pull_request_number"] != float64(1) || shipper.runID != "run-1" || shipper.branch != "eggy/run-1" || shipper.commitMessage != "fix: tests" {
		t.Fatalf("modified=%s shipper=%#v err=%v", modified, shipper, err)
	}
	if _, err := byName["repository_modify"].Execute(context.Background(), json.RawMessage(`{"repository":"missing","instruction":"fix tests"}`)); err == nil {
		t.Fatal("expected unknown repository error")
	}
}

func TestRepositoryModifyStampsRunIDOnProgressEvents(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	worker := &fakeRepositoryWorker{}
	shipper := &fakeShipper{}
	var received []ports.CodingProgress
	tools := NewRepositoryTools(store, worker, shipper, func() string { return "run-42" },
		func(progress ports.CodingProgress) { received = append(received, progress) })
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

func TestRepositoryContinueResumesNamedRunAndShipsIt(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	worker := &fakeRepositoryWorker{}
	shipper := &fakeShipper{pr: ports.PullRequest{URL: "https://example.com/pr/9", Number: 9}}
	tools := NewRepositoryTools(store, worker, shipper, func() string { return "unused" }, nil)
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	result, err := byName["repository_continue"].Execute(context.Background(), json.RawMessage(`{"run_id":"run-9","instruction":"fix the next failing test"}`))
	if err != nil || !strings.Contains(string(result), `"run_id":"run-9"`) || shipper.runID != "run-9" || worker.resumedRunID != "run-9" || worker.resumedInstruction != "fix the next failing test" {
		t.Fatalf("result=%s shipper=%#v worker=%#v err=%v", result, shipper, worker, err)
	}
}

func TestRepositoryModifyReportsPartialShipNote(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	worker := &fakeRepositoryWorker{}
	shipper := &fakeShipper{note: "Committed. Push is unavailable for the configured repository provider."}
	tools := NewRepositoryTools(store, worker, shipper, func() string { return "run-1" }, nil)
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	result, err := byName["repository_modify"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","instruction":"fix tests"}`))
	var modifyResult map[string]any
	_ = json.Unmarshal(result, &modifyResult)
	if err != nil || modifyResult["status"] != "partial" || modifyResult["note"] != shipper.note {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

func TestRepositoryListReportsNotConfigured(t *testing.T) {
	tools := NewRepositoryTools(newMemoryStore(), &fakeRepositoryWorker{}, &fakeShipper{}, func() string { return "run" }, nil)
	result, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(string(result), `"status":"not_configured"`) {
		t.Fatalf("result=%s err=%v", result, err)
	}
}

type fakeRepositoryWorker struct {
	resumedRunID       string
	resumedInstruction string
}

func (w *fakeRepositoryWorker) Start(_ context.Context, runID string, repository ports.Repository, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "working"})
	}
	return ports.CodingRun{ID: runID, Repository: repository.Name, Branch: "eggy/" + runID, BaseRevision: "abc123"}, ports.CodingResult{Summary: "fixed", Validation: "tests pass", CommitMessage: "fix: tests", ChangedFiles: []string{"main.go"}}, nil
}

func (w *fakeRepositoryWorker) Resume(_ context.Context, runID, instruction string, progress func(ports.CodingProgress)) (ports.CodingRun, ports.CodingResult, error) {
	w.resumedRunID, w.resumedInstruction = runID, instruction
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "resuming"})
	}
	return ports.CodingRun{ID: runID, Repository: "eggy", Branch: "eggy/" + runID, BaseRevision: "abc123"}, ports.CodingResult{Summary: "continued", Validation: "tests pass", CommitMessage: "fix: continue", ChangedFiles: []string{"main.go"}}, nil
}

type fakeShipper struct {
	pr            ports.PullRequest
	note          string
	runID         string
	branch        string
	commitMessage string
}

func (s *fakeShipper) Ship(_ context.Context, runID, branch, commitMessage string) (ports.PullRequest, string, error) {
	s.runID, s.branch, s.commitMessage = runID, branch, commitMessage
	return s.pr, s.note, nil
}
