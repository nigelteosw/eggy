package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/runner/localprocess"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestGitRepositoryCloneInspectDiffCommitAndPush(t *testing.T) {
	remote := createRemote(t)
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := localprocess.New(root, []string{"PATH", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}, 10*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := runner.Create(context.Background(), "run-1")
	adapter := New(runner, "sensitive-token", "https://api.github.test", http.DefaultClient)
	repository := ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "main", ProtectedBranches: []string{"main"}}
	if err := adapter.Clone(context.Background(), repository, workspace); err != nil {
		t.Fatal(err)
	}
	guidance, err := adapter.Inspect(context.Background(), workspace)
	if err != nil || guidance != "# Agent guidance\n" {
		t.Fatalf("guidance=%q err=%v", guidance, err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "NEW.md"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := adapter.Diff(context.Background(), workspace)
	if err != nil || !strings.Contains(diff, "+changed") || !strings.Contains(diff, "+new file") {
		t.Fatalf("diff=%q err=%v", diff, err)
	}
	commit, err := adapter.Commit(context.Background(), workspace, "feat: change readme")
	if err != nil || len(commit) < 7 {
		t.Fatalf("commit=%q err=%v", commit, err)
	}
	if _, err := runner.Execute(context.Background(), ports.Command{Argv: []string{"git", "checkout", "-b", "feature"}, Dir: workspace}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Push(context.Background(), workspace, "feature"); err != nil {
		t.Fatal(err)
	}
	revision, err := adapter.WorkspaceRevision(context.Background(), workspace)
	if err != nil || revision.Branch != "feature" || revision.Head != commit {
		t.Fatalf("revision=%#v err=%v", revision, err)
	}
	if output := git(t, remote, "branch", "--list", "feature"); !strings.Contains(output, "feature") {
		t.Fatalf("remote branches=%q", output)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".eggy-askpass-*"))
	if len(matches) != 0 {
		t.Fatalf("askpass leaked: %v", matches)
	}
}

func TestCheckRemoteValidatesReachabilityAndBaseBranch(t *testing.T) {
	remote := createRemote(t)
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := localprocess.New(root, []string{"PATH", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}, 10*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(runner, "sensitive-token", "https://api.github.test", http.DefaultClient)

	workspace, _ := runner.Create(context.Background(), "check-1")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "main"}, workspace); err != nil {
		t.Fatalf("expected reachable remote with main branch, got %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".eggy-askpass-*"))
	if len(matches) != 0 {
		t.Fatalf("askpass leaked: %v", matches)
	}

	workspace, _ = runner.Create(context.Background(), "check-2")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "does-not-exist"}, workspace); err == nil {
		t.Fatal("expected error for missing base branch")
	}

	workspace, _ = runner.Create(context.Background(), "check-3")
	if err := adapter.CheckRemote(context.Background(), ports.Repository{Name: "repo", CloneURL: filepath.Join(t.TempDir(), "nowhere"), BaseBranch: "main"}, workspace); err == nil {
		t.Fatal("expected error for unreachable remote")
	}
}

func TestGitHubCreatesPullRequestWithHeaderOnlyCredential(t *testing.T) {
	var gotPath, gotAuthorization string
	var gotBody map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath, gotAuthorization = request.URL.Path, request.Header.Get("Authorization")
		_ = json.NewDecoder(request.Body).Decode(&gotBody)
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"html_url":"https://github.test/acme/repo/pull/12","number":12}`))}, nil
	})}
	adapter := New(nil, "sensitive-token", "https://api.github.test", client)
	pr, err := adapter.CreatePullRequest(context.Background(), ports.Repository{CloneURL: "https://github.com/acme/repo.git", BaseBranch: "main"}, "feature", "Title", "Body")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/acme/repo/pulls" || gotAuthorization != "Bearer sensitive-token" || gotBody["head"] != "feature" || pr.Number != 12 {
		t.Fatalf("path=%q auth=%q body=%#v pr=%#v", gotPath, gotAuthorization, gotBody, pr)
	}
	encoded, _ := json.Marshal(gotBody)
	if strings.Contains(string(encoded), "sensitive-token") {
		t.Fatalf("token leaked in body: %s", encoded)
	}
}

func TestFindOpenPullRequestReturnsTheOpenPullRequestForTheBranch(t *testing.T) {
	var gotPath, gotQuery string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotPath, gotQuery = request.URL.Path, request.URL.RawQuery
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"number":7,"html_url":"https://github.test/acme/repo/pull/7"}]`))}, nil
	})}
	adapter := New(nil, "token", "https://api.github.test", client)
	pr, found, err := adapter.FindOpenPullRequest(context.Background(), ports.Repository{CloneURL: "https://github.com/acme/repo.git", BaseBranch: "main"}, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if !found || pr.Number != 7 || pr.URL != "https://github.test/acme/repo/pull/7" {
		t.Fatalf("found=%v pr=%#v", found, pr)
	}
	if gotPath != "/repos/acme/repo/pulls" || !strings.Contains(gotQuery, "state=open") || !strings.Contains(gotQuery, "head=acme%3Afeature") {
		t.Fatalf("path=%q query=%q", gotPath, gotQuery)
	}
}

func TestFindOpenPullRequestReportsNotFoundWhenNoneAreOpen(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}
	adapter := New(nil, "token", "https://api.github.test", client)
	pr, found, err := adapter.FindOpenPullRequest(context.Background(), ports.Repository{CloneURL: "https://github.com/acme/repo.git", BaseBranch: "main"}, "feature")
	if err != nil || found {
		t.Fatalf("found=%v pr=%#v err=%v", found, pr, err)
	}
}

func TestUpdatePullRequestBodyAppendsToTheExistingDescription(t *testing.T) {
	var patchedBody map[string]any
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"body":"Original description."}`))}, nil
		}
		if request.Method != http.MethodPatch {
			t.Fatalf("unexpected method %s", request.Method)
		}
		_ = json.NewDecoder(request.Body).Decode(&patchedBody)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	adapter := New(nil, "token", "https://api.github.test", client)
	if err := adapter.UpdatePullRequestBody(context.Background(), ports.Repository{CloneURL: "https://github.com/acme/repo.git"}, 7, "Updated after another round."); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want a GET then a PATCH", calls)
	}
	body, _ := patchedBody["body"].(string)
	if !strings.Contains(body, "Original description.") || !strings.Contains(body, "Updated after another round.") {
		t.Fatalf("patched body=%q", body)
	}
}

func TestRepositoryCapabilitiesReflectServerSideCredentialReadiness(t *testing.T) {
	withoutToken := New(nil, "", "https://api.github.test", http.DefaultClient).RepositoryCapabilities()
	if !withoutToken.Commit || withoutToken.Push || withoutToken.PullRequest {
		t.Fatalf("without token=%#v", withoutToken)
	}
	withToken := New(nil, "sensitive-token", "https://api.github.test", http.DefaultClient).RepositoryCapabilities()
	if !withToken.Commit || !withToken.Push || !withToken.PullRequest {
		t.Fatalf("with token=%#v", withToken)
	}
}

func TestDiffRejectsTruncatedApprovalMaterial(t *testing.T) {
	adapter := New(truncatedRunner{}, "token", "https://api.github.test", http.DefaultClient)
	if _, err := adapter.Diff(context.Background(), "/tmp/run"); !errors.Is(err, ErrDiffTooLarge) {
		t.Fatalf("error=%v", err)
	}
}

func TestReadOnlyWorkspaceOperationsListSearchReadStatusAndBranches(t *testing.T) {
	remote := createRemote(t)
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := localprocess.New(root, []string{"PATH", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}, 10*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	workspace, _ := runner.Create(context.Background(), "read-1")
	adapter := New(runner, "token", "https://api.github.test", http.DefaultClient)
	repository := ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "main"}
	if err := adapter.Clone(context.Background(), repository, workspace); err != nil {
		t.Fatal(err)
	}

	entries, err := adapter.ListTree(context.Background(), workspace, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawReadme, sawGit bool
	for _, entry := range entries {
		if entry.Path == "README.md" {
			sawReadme = true
		}
		if strings.HasPrefix(entry.Path, ".git") {
			sawGit = true
		}
	}
	if !sawReadme || sawGit {
		t.Fatalf("entries=%#v", entries)
	}

	matches, err := adapter.Search(context.Background(), workspace, "initial", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "README.md" || matches[0].Line != 1 {
		t.Fatalf("matches=%#v", matches)
	}

	content, err := adapter.ReadFile(context.Background(), workspace, "README.md", 0, 0)
	if err != nil || content != "initial\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}

	if _, err := adapter.ReadFile(context.Background(), workspace, "../outside.md", 0, 0); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if _, err := adapter.ReadFile(context.Background(), workspace, "/etc/passwd", 0, 0); err == nil {
		t.Fatal("expected absolute escape to be rejected")
	}

	status, err := adapter.Status(context.Background(), workspace)
	if err != nil {
		t.Fatalf("status err=%v", err)
	}
	if !strings.Contains(status, "## main") {
		t.Fatalf("status=%q", status)
	}

	branches, err := adapter.Branches(context.Background(), workspace)
	if err != nil || len(branches) == 0 {
		t.Fatalf("branches=%v err=%v", branches, err)
	}
}

func TestGitHubMetadataReadsRepositoryIssuePullRequestAndChecks(t *testing.T) {
	repository := ports.Repository{CloneURL: "https://github.com/acme/repo.git"}
	for _, test := range []struct {
		name     string
		wantPath string
		call     func(adapter *Adapter) error
		body     string
	}{
		{
			name: "repository", wantPath: "/repos/acme/repo",
			body: `{"description":"desc","default_branch":"main","private":true,"html_url":"https://github.com/acme/repo"}`,
			call: func(adapter *Adapter) error {
				summary, err := adapter.RepositorySummary(context.Background(), repository)
				if err == nil && (summary.DefaultBranch != "main" || !summary.Private) {
					t.Fatalf("summary=%#v", summary)
				}
				return err
			},
		},
		{
			name: "issue", wantPath: "/repos/acme/repo/issues/7",
			body: `{"number":7,"title":"Bug","state":"open","body":"details","html_url":"https://github.com/acme/repo/issues/7"}`,
			call: func(adapter *Adapter) error {
				summary, err := adapter.Issue(context.Background(), repository, 7)
				if err == nil && (summary.Number != 7 || summary.Title != "Bug") {
					t.Fatalf("summary=%#v", summary)
				}
				return err
			},
		},
		{
			name: "pull_request", wantPath: "/repos/acme/repo/pulls/9",
			body: `{"number":9,"title":"Feature","state":"open","body":"details","html_url":"https://github.com/acme/repo/pull/9"}`,
			call: func(adapter *Adapter) error {
				summary, err := adapter.PullRequestSummary(context.Background(), repository, 9)
				if err == nil && (summary.Number != 9 || summary.Title != "Feature") {
					t.Fatalf("summary=%#v", summary)
				}
				return err
			},
		},
		{
			name: "checks", wantPath: "/repos/acme/repo/commits/abc123/check-runs",
			body: `{"check_runs":[{"name":"build","status":"completed","conclusion":"success","html_url":"https://github.com/acme/repo/runs/1"}]}`,
			call: func(adapter *Adapter) error {
				checks, err := adapter.Checks(context.Background(), repository, "abc123")
				if err == nil && (len(checks) != 1 || checks[0].Name != "build" || checks[0].Conclusion != "success") {
					t.Fatalf("checks=%#v", checks)
				}
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotPath, gotAuthorization string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				gotPath, gotAuthorization = request.URL.Path, request.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			adapter := New(nil, "sensitive-token", "https://api.github.test", client)
			if err := test.call(adapter); err != nil {
				t.Fatal(err)
			}
			if gotPath != test.wantPath || gotAuthorization != "Bearer sensitive-token" {
				t.Fatalf("path=%q auth=%q", gotPath, gotAuthorization)
			}
		})
	}
}

func createRemote(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "-b", "main", source)
	git(t, source, "config", "user.name", "Test")
	git(t, source, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "AGENTS.md"), []byte("# Agent guidance\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "initial")
	git(t, "", "clone", "--bare", source, remote)
	return remote
}

func git(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type truncatedRunner struct{}

func (truncatedRunner) Create(context.Context, string) (string, error) { return "", nil }
func (truncatedRunner) Destroy(context.Context, string) error          { return nil }
func (truncatedRunner) Execute(_ context.Context, command ports.Command) (ports.CommandResult, error) {
	if len(command.Argv) > 1 && command.Argv[1] == "add" {
		return ports.CommandResult{}, nil
	}
	return ports.CommandResult{Stdout: "partial", OutputTruncated: true}, nil
}
