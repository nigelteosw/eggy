package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryReadToolsCloneIntoEphemeralWorkspaceAndDestroyIt(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/read-1"}
	reader := &fakeRepositoryReader{
		entries:  []ports.WorkspaceEntry{{Path: "README.md"}},
		matches:  []ports.WorkspaceMatch{{Path: "README.md", Line: 1, Text: "hello"}},
		content:  "line one\n",
		status:   "## main",
		branches: []string{"main"},
		summary:  ports.RepositorySummary{Title: "eggy", DefaultBranch: "main"},
		checks:   []ports.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}},
	}
	tools := NewRepositoryReadTools(store, runner, reader, reader, func() string { return "run-1" })
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}

	tree, err := byName["repository_tree"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy"}`))
	if err != nil || !strings.Contains(string(tree), "README.md") {
		t.Fatalf("tree=%s err=%v", tree, err)
	}
	search, err := byName["repository_search"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","query":"hello"}`))
	if err != nil || !strings.Contains(string(search), "hello") {
		t.Fatalf("search=%s err=%v", search, err)
	}
	read, err := byName["repository_read"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","path":"README.md"}`))
	if err != nil || !strings.Contains(string(read), "line one") {
		t.Fatalf("read=%s err=%v", read, err)
	}
	status, err := byName["repository_status"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy"}`))
	if err != nil || !strings.Contains(string(status), "## main") {
		t.Fatalf("status=%s err=%v", status, err)
	}
	summary, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"repository"}`))
	if err != nil || !strings.Contains(string(summary), "eggy") {
		t.Fatalf("summary=%s err=%v", summary, err)
	}
	checks, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"checks","ref":"abc123"}`))
	if err != nil || !strings.Contains(string(checks), "build") {
		t.Fatalf("checks=%s err=%v", checks, err)
	}
	if reader.cloned != 4 {
		t.Fatalf("expected 4 clones for the workspace-bound tools (tree, search, read, status); repository_github must not clone, got %d", reader.cloned)
	}
	if !runner.created || !runner.destroyed {
		t.Fatalf("runner=%#v", runner)
	}
}

func TestRepositoryReadToolsRejectUnknownRepositoryAndUnsupportedKind(t *testing.T) {
	store := newMemoryStore()
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/read-2"}
	reader := &fakeRepositoryReader{}
	tools := NewRepositoryReadTools(store, runner, reader, reader, func() string { return "run-2" })
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	if _, err := byName["repository_tree"].Execute(context.Background(), json.RawMessage(`{"repository":"missing"}`)); err == nil {
		t.Fatal("expected unknown repository error")
	}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	if _, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"issue"}`)); err == nil || !strings.Contains(err.Error(), "number is required") {
		t.Fatalf("error=%v", err)
	}
	if _, err := byName["repository_github"].Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"bogus"}`)); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("error=%v", err)
	}
}

type fakeRepositoryReader struct {
	cloned   int
	entries  []ports.WorkspaceEntry
	matches  []ports.WorkspaceMatch
	content  string
	status   string
	branches []string
	summary  ports.RepositorySummary
	checks   []ports.CheckRun
}

func (r *fakeRepositoryReader) Clone(context.Context, ports.Repository, string) error {
	r.cloned++
	return nil
}
func (r *fakeRepositoryReader) Inspect(context.Context, string) (string, error) { return "", nil }
func (r *fakeRepositoryReader) CreateBranch(context.Context, string, string) error {
	return errors.New("read tools must never create a branch")
}
func (r *fakeRepositoryReader) Diff(context.Context, string) (string, error) { return "", nil }

func (r *fakeRepositoryReader) ListTree(context.Context, string, string, int) ([]ports.WorkspaceEntry, error) {
	return r.entries, nil
}
func (r *fakeRepositoryReader) Search(context.Context, string, string, int) ([]ports.WorkspaceMatch, error) {
	return r.matches, nil
}
func (r *fakeRepositoryReader) ReadFile(context.Context, string, string, int, int) (string, error) {
	return r.content, nil
}
func (r *fakeRepositoryReader) Status(context.Context, string) (string, error) { return r.status, nil }
func (r *fakeRepositoryReader) Branches(context.Context, string) ([]string, error) {
	return r.branches, nil
}
func (r *fakeRepositoryReader) RepositorySummary(context.Context, ports.Repository) (ports.RepositorySummary, error) {
	return r.summary, nil
}
func (r *fakeRepositoryReader) Issue(context.Context, ports.Repository, int) (ports.RepositorySummary, error) {
	return r.summary, nil
}
func (r *fakeRepositoryReader) PullRequestSummary(context.Context, ports.Repository, int) (ports.RepositorySummary, error) {
	return r.summary, nil
}
func (r *fakeRepositoryReader) Checks(context.Context, ports.Repository, string) ([]ports.CheckRun, error) {
	return r.checks, nil
}
