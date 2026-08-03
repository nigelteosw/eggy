package github

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/runner/localprocess"
)

func TestReadFileReadsAClonedWorkspaceAndRefusesToEscapeIt(t *testing.T) {
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

	content, err := adapter.ReadFile(context.Background(), workspace, "README.md", 0, 0)
	if err != nil || content != "initial\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if _, err := adapter.ReadFile(context.Background(), workspace, "../outside.md", 0, 0); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestValidateCloneAccessChecksTheConfiguredBranch(t *testing.T) {
	remote := createRemote(t)
	root := filepath.Join(t.TempDir(), "runs")
	runner, err := localprocess.New(root, []string{"PATH", "GIT_ASKPASS", "EGGY_GITHUB_TOKEN", "GIT_TERMINAL_PROMPT"}, 10*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(runner, "token", "https://api.github.test", http.DefaultClient)
	if err := adapter.ValidateCloneAccess(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "main"}); err != nil {
		t.Fatalf("validate configured branch: %v", err)
	}
	if err := adapter.ValidateCloneAccess(context.Background(), ports.Repository{Name: "repo", CloneURL: remote, BaseBranch: "missing"}); err == nil {
		t.Fatal("expected missing configured branch to fail validation")
	}
}

func TestGitHubMetadataReadsRepositoryIssuePullRequestAndChecks(t *testing.T) {
	repository := ports.Repository{CloneURL: "https://github.com/acme/repo.git"}
	for _, test := range []struct {
		name, wantPath, body string
		call                 func(*Adapter) error
	}{
		{"repository", "/repos/acme/repo", `{"description":"desc","default_branch":"main","private":true,"html_url":"https://github.com/acme/repo"}`, func(adapter *Adapter) error {
			summary, err := adapter.RepositorySummary(context.Background(), repository)
			if err == nil && (summary.DefaultBranch != "main" || !summary.Private) {
				t.Fatalf("summary=%#v", summary)
			}
			return err
		}},
		{"issue", "/repos/acme/repo/issues/7", `{"number":7,"title":"Bug","state":"open","body":"details","html_url":"https://github.com/acme/repo/issues/7"}`, func(adapter *Adapter) error {
			summary, err := adapter.Issue(context.Background(), repository, 7)
			if err == nil && (summary.Number != 7 || summary.Title != "Bug") {
				t.Fatalf("summary=%#v", summary)
			}
			return err
		}},
		{"pull_request", "/repos/acme/repo/pulls/9", `{"number":9,"title":"Feature","state":"open","body":"details","html_url":"https://github.com/acme/repo/pull/9"}`, func(adapter *Adapter) error {
			summary, err := adapter.ReviewSummary(context.Background(), repository, 9)
			if err == nil && (summary.Number != 9 || summary.Title != "Feature") {
				t.Fatalf("summary=%#v", summary)
			}
			return err
		}},
		{"checks", "/repos/acme/repo/commits/abc123/check-runs", `{"check_runs":[{"name":"build","status":"completed","conclusion":"success","html_url":"https://github.com/acme/repo/runs/1"}]}`, func(adapter *Adapter) error {
			checks, err := adapter.Checks(context.Background(), repository, "abc123")
			if err == nil && (len(checks) != 1 || checks[0].Name != "build" || checks[0].Conclusion != "success") {
				t.Fatalf("checks=%#v", checks)
			}
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotPath, gotAuthorization string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				gotPath, gotAuthorization = request.URL.Path, request.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			if err := test.call(New(nil, "sensitive-token", "https://api.github.test", client)); err != nil {
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
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "initial")
	git(t, "", "clone", "--bare", source, remote)
	return remote
}

func git(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
