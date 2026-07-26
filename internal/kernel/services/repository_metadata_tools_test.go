package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestRepositoryMetadataToolsReadGitHubWithoutCloning(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	reader := &fakeRepositoryReader{
		summary: ports.RepositorySummary{Title: "eggy", DefaultBranch: "main"},
		checks:  []ports.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}},
	}
	tools := NewRepositoryMetadataTools(store, reader)
	if len(tools) != 1 || tools[0].Definition().Name != "repository_github" {
		t.Fatalf("metadata tools must be repository_github alone, got %#v", tools)
	}
	metadata := tools[0]

	summary, err := metadata.Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"repository"}`))
	if err != nil || !strings.Contains(string(summary), "eggy") {
		t.Fatalf("summary=%s err=%v", summary, err)
	}
	checks, err := metadata.Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"checks","ref":"abc123"}`))
	if err != nil || !strings.Contains(string(checks), "build") {
		t.Fatalf("checks=%s err=%v", checks, err)
	}
	if reader.cloned != 0 {
		t.Fatalf("repository_github must never clone, got %d clones", reader.cloned)
	}
}

func TestRepositoryMetadataToolsRejectUnknownRepositoryAndUnsupportedKind(t *testing.T) {
	store := newMemoryStore()
	metadata := NewRepositoryMetadataTools(store, &fakeRepositoryReader{})[0]
	if _, err := metadata.Execute(context.Background(), json.RawMessage(`{"repository":"missing","kind":"repository"}`)); err == nil {
		t.Fatal("expected unknown repository error")
	}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	if _, err := metadata.Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"issue"}`)); err == nil || !strings.Contains(err.Error(), "number is required") {
		t.Fatalf("error=%v", err)
	}
	if _, err := metadata.Execute(context.Background(), json.RawMessage(`{"repository":"eggy","kind":"bogus"}`)); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("error=%v", err)
	}
}

type fakeReadWorkspaceRunner struct {
	workspace          string
	created, destroyed bool
	command            ports.Command
	commandResult      ports.CommandResult
}

func (r *fakeReadWorkspaceRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *fakeReadWorkspaceRunner) Execute(_ context.Context, command ports.Command) (ports.CommandResult, error) {
	r.command = command
	return r.commandResult, nil
}
func (r *fakeReadWorkspaceRunner) Destroy(context.Context, string) error {
	r.destroyed = true
	return nil
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
	return errors.New("inspection checkouts must never create a branch")
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
