package agentrunner

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

const testSecret = "sk-live-test-secret-value"

// pubEnv is a publisher under test: a local bare remote standing in for
// GitHub's git host, and a fake GitHub API recording every pull request.
type pubEnv struct {
	t       *testing.T
	svc     *Service
	store   *memStore
	bare    string
	base    string
	now     time.Time
	mu      sync.Mutex
	prs     []map[string]any // pull requests opened through the API
	protect int              // status returned by the branch protection call
	notes   []string
	opens   int // POST /pulls calls
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newBareRemote makes a bare remote with one commit on main, standing in for
// GitHub's git host, and returns its path and the base commit.
func newBareRemote(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	gitT(t, dir, "init", "--bare", "-b", "main", bare)
	gitT(t, dir, "init", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-m", "init")
	gitT(t, work, "push", bare, "main")
	return bare, gitT(t, bare, "rev-parse", "main")
}

func newPubEnv(t *testing.T) *pubEnv {
	t.Helper()
	p := &pubEnv{t: t, now: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), protect: http.StatusOK}
	dir := t.TempDir()
	p.bare, p.base = newBareRemote(t)

	gh := NewGitHub(nil)
	srv := httptest.NewServer(http.HandlerFunc(p.api))
	t.Cleanup(srv.Close)
	gh.baseURL = srv.URL

	creds, err := NewCredentials(newMemRecords(), testEncryptionKey, gh)
	if err != nil {
		t.Fatal(err)
	}
	p.store = newMemStore()
	repos := []config.RepositoryConfig{{Name: "r", CloneURL: "https://github.com/o/r.git", BaseBranch: "main", Checks: [][]string{{"go", "test"}}}}
	p.svc, err = New(Options{
		Store: p.store, Credentials: creds, GitHub: gh,
		Workspaces:   NewWorkspaces(filepath.Join(dir, "runs"), &fakeRunner{}),
		Repositories: func() []config.RepositoryConfig { return repos },
		Models:       map[string]agent.ModelTarget{"coder": {Model: &fakeModel{}}},
		Model:        "coder",
		Now:          func() time.Time { return p.now },
		GitURL:       func(config.RepositoryConfig) string { return p.bare },
		SecretValues: func() []string { return []string{testSecret} },
		Notify: func(_ context.Context, _ ports.AgentRun, text string) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.notes = append(p.notes, text)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := creds.Set(acct("a"), "ghp_publish_secret", repos); err != nil {
		t.Fatal(err)
	}
	return p
}

// api is the slice of the GitHub API the publisher calls, with the same
// status codes the real endpoints return for these requests.
func (p *pubEnv) api(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case r.URL.Path == "/user":
		_, _ = w.Write([]byte(`{"login":"nigel","id":42}`))
	case r.URL.Path == "/repos/o/r":
		_, _ = w.Write([]byte(`{"permissions":{"push":true}}`))
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/branches/"):
		w.WriteHeader(p.protect)
		if p.protect == http.StatusOK {
			_, _ = w.Write([]byte(`{"protected":false}`))
		}
	case r.URL.Path == "/repos/o/r/pulls" && r.Method == http.MethodGet:
		var matches []map[string]any
		for _, pr := range p.prs {
			matches = append(matches, pr)
		}
		_ = json.NewEncoder(w).Encode(matches)
	case r.URL.Path == "/repos/o/r/pulls" && r.Method == http.MethodPost:
		raw, _ := io.ReadAll(r.Body)
		var pr map[string]any
		_ = json.Unmarshal(raw, &pr)
		p.opens++
		pr["html_url"] = "https://github.com/o/r/pull/" + string(rune('0'+p.opens))
		p.prs = append(p.prs, map[string]any{"html_url": pr["html_url"], "body": pr["body"]})
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": pr["html_url"]})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newRun stores a run that has finished its coding turn and is ready to publish,
// with its workspace holding the agent's changes.
func (p *pubEnv) newRun(id string) ports.AgentRun {
	p.t.Helper()
	ctx := acct("a")
	run := ports.AgentRun{
		ID: id, Repository: "r", Brief: "update the readme", BriefDigest: "d", BaseBranch: "main",
		BaseSHA: p.base, Branch: "eggy/" + id, State: ports.AgentRunRunning, PATGeneration: 1,
		MandateExpires: p.now.Add(24 * time.Hour), CreatedAt: p.now, UpdatedAt: p.now,
	}
	if err := p.store.CreateAgentRun(ctx, run); err != nil {
		p.t.Fatal(err)
	}
	stored, err := p.store.GetAgentRun(ctx, id)
	if err != nil {
		p.t.Fatal(err)
	}
	seedWorkspace(p.t, p.svc.opts.Workspaces.Dir(id))
	return stored
}

// seedWorkspace makes a run's workspace the checkout a real clone would give:
// the base README, unchanged. An empty workspace would read as deleting every
// base file.
func seedWorkspace(t *testing.T, runDir string) {
	t.Helper()
	if err := makeRunDirs(runDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "ws", "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (p *pubEnv) ws(id string) string { return filepath.Join(p.svc.opts.Workspaces.Dir(id), "ws") }

func (p *pubEnv) writeWS(id, rel, body string) {
	p.t.Helper()
	path := filepath.Join(p.ws(id), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pubEnv) publish(run ports.AgentRun, results []CheckResult) {
	p.t.Helper()
	repo, _ := p.svc.repository("r")
	if err := p.svc.publish(acct("a"), run, repo, results, "changed the readme"); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pubEnv) state(id string) ports.AgentRun {
	p.t.Helper()
	run, err := p.store.GetAgentRun(acct("a"), id)
	if err != nil {
		p.t.Fatal(err)
	}
	return run
}

var passingGoTest = []CheckResult{{Argv: []string{"go", "test"}, ExitCode: 0}}

func TestPublishOpensPRWithCheckEvidence(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("0000000000000000000000000000000a")
	p.writeWS(run.ID, "README.md", "changed\n")
	p.publish(run, passingGoTest)
	got := p.state(run.ID)
	if got.State != ports.AgentRunCompleted || got.PRURL == "" || got.Draft {
		t.Fatalf("run = %+v", got)
	}
	if p.opens != 1 {
		t.Fatalf("opened %d PRs, want 1", p.opens)
	}
	if body := p.prs[0]["body"].(string); !strings.Contains(body, "go test") || !strings.Contains(body, "exit 0") || !strings.Contains(body, "<!-- eggy-run:"+run.ID+" -->") {
		t.Fatalf("PR body lacks check evidence or marker:\n%s", body)
	}
	if gitT(t, p.bare, "show", "eggy/"+run.ID+":README.md") != "changed" {
		t.Fatal("pushed branch does not carry the change")
	}
	if author := gitT(t, p.bare, "log", "-1", "--format=%an <%ae>", "eggy/"+run.ID); author != "nigel <42+nigel@users.noreply.github.com>" {
		t.Fatalf("author = %q", author)
	}
}

func TestPublishDraftWhenChecksFailOrMissing(t *testing.T) {
	p := newPubEnv(t)
	missing := p.newRun("0000000000000000000000000000000b")
	p.writeWS(missing.ID, "README.md", "a\n")
	p.publish(missing, nil)
	if got := p.state(missing.ID); !got.Draft {
		t.Fatal("no checks ran, but the PR is not a draft")
	}
	failing := p.newRun("0000000000000000000000000000000c")
	p.writeWS(failing.ID, "README.md", "b\n")
	p.publish(failing, []CheckResult{{Argv: []string{"go", "test"}, ExitCode: 1}})
	if got := p.state(failing.ID); !got.Draft {
		t.Fatal("a failing check produced a regular PR")
	}
}

func TestPublishNoChangeNoPR(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("0000000000000000000000000000000d")
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunCompleted || got.PRURL != "" || p.opens != 0 {
		t.Fatalf("no change: run = %+v opens = %d", got, p.opens)
	}
}

func TestPublishRejectsGithubDir(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("0000000000000000000000000000000e")
	p.writeWS(run.ID, ".github/workflows/ci.yml", "on: push\n")
	p.publish(run, passingGoTest)
	got := p.state(run.ID)
	if got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, ".github") || p.opens != 0 {
		t.Fatalf("run = %+v opens = %d", got, p.opens)
	}
}

func TestPublishRejectsSymlink(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("0000000000000000000000000000000f")
	if err := os.Symlink("/etc/passwd", filepath.Join(p.ws(run.ID), "link")); err != nil {
		t.Fatal(err)
	}
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "symlink") {
		t.Fatalf("run = %+v", got)
	}
}

func TestPublishRejectsSecretInDiff(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000010")
	p.writeWS(run.ID, "config.txt", "key="+testSecret+"\n")
	p.publish(run, passingGoTest)
	got := p.state(run.ID)
	if got.State != ports.AgentRunFailed || strings.Contains(got.Failure, testSecret) || p.opens != 0 {
		t.Fatalf("secret was not refused cleanly: %+v", got)
	}
}

// The agent controls ws/.git. A config there that would run code if git read
// it must do nothing, because the publisher never runs git in the workspace.
func TestPublishNeverReadsWorkspaceGit(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000011")
	gitDir := filepath.Join(p.ws(run.ID), ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	trap := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n\tfsmonitor = touch "+trap+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.writeWS(run.ID, "README.md", "changed\n")
	p.publish(run, passingGoTest)
	if _, err := os.Stat(trap); err == nil {
		t.Fatal("a command in the workspace's git config ran")
	}
	if got := p.state(run.ID); got.PRURL == "" {
		t.Fatalf("publish failed: %+v", got)
	}
}

func TestPublishRefusesStaleGeneration(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000012")
	run.PATGeneration = 0
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "token changed") {
		t.Fatalf("run = %+v", got)
	}
}

func TestPublishRefusesExpiredMandate(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000013")
	p.now = p.now.Add(25 * time.Hour)
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunFailed || !strings.Contains(got.Failure, "mandate") {
		t.Fatalf("run = %+v", got)
	}
}

func TestPublishFailsClosedWhenProtectionUnknown(t *testing.T) {
	p := newPubEnv(t)
	p.protect = http.StatusInternalServerError
	run := p.newRun("00000000000000000000000000000014")
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunFailed {
		t.Fatalf("run = %+v", got)
	}
	// rev-parse --verify exits 0 only when the branch exists on the remote.
	if exec.Command("git", "-C", p.bare, "rev-parse", "--verify", "refs/heads/eggy/"+run.ID).Run() == nil {
		t.Fatal("pushed although protection could not be checked")
	}
}

// A remote that refuses the push models revoked access. The run fails and its
// workspace is kept for the owner.
func TestPublishRevokedAccessFails(t *testing.T) {
	p := newPubEnv(t)
	hook := filepath.Join(p.bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho denied >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := p.newRun("00000000000000000000000000000015")
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunFailed || got.PRURL != "" {
		t.Fatalf("run = %+v", got)
	}
	if _, err := os.Stat(p.ws(run.ID)); err != nil {
		t.Fatal("workspace was removed after a failed push")
	}
}

// After a crash between the PR request and its record, reconcile finds the
// existing PR by its marker instead of opening a second one.
func TestReconcileDoesNotDuplicatePR(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000016")
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	first := p.state(run.ID)
	// Pretend the PR's URL was never recorded, as after a crash.
	crashed := first
	crashed.State = ports.AgentRunPublishing
	crashed.Stage = ports.PublishPRIntended
	crashed.PRURL = ""
	crashed.Revision = first.Revision
	if _, err := p.store.UpdateAgentRun(acct("a"), crashed); err != nil {
		t.Fatal(err)
	}
	opensBefore := p.opens
	if err := p.svc.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.opens != opensBefore {
		t.Fatalf("reconcile opened a second PR (%d -> %d)", opensBefore, p.opens)
	}
	if got := p.state(run.ID); got.PRURL == "" || got.State != ports.AgentRunCompleted {
		t.Fatalf("run = %+v", got)
	}
}

// A PR that is recorded but not yet announced is announced again, and nothing
// is republished.
func TestNotifyRetryDoesNotRepublish(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000017")
	p.writeWS(run.ID, "README.md", "x\n")
	p.publish(run, passingGoTest)
	opensBefore := p.opens
	delivered := p.state(run.ID)
	delivered.State = ports.AgentRunPublishing
	delivered.Stage = ports.PublishPROpened
	delivered.Delivered = false
	if _, err := p.store.UpdateAgentRun(acct("a"), delivered); err != nil {
		t.Fatal(err)
	}
	if err := p.svc.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.opens != opensBefore {
		t.Fatal("a notify retry opened another PR")
	}
	if got := p.state(run.ID); !got.Delivered || got.Stage != ports.PublishDelivered {
		t.Fatalf("run = %+v", got)
	}
}

// The summary is the model's own text. A secret in it must not reach the pull
// request body, which GitHub stores and shows to everyone with access.
func TestPublishRedactsSecretFromPRBody(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000018")
	p.writeWS(run.ID, "README.md", "changed\n")
	repo, _ := p.svc.repository("r")
	if err := p.svc.publish(acct("a"), run, repo, passingGoTest, "updated the readme with "+testSecret); err != nil {
		t.Fatal(err)
	}
	if p.opens != 1 {
		t.Fatalf("opened %d PRs, want 1", p.opens)
	}
	if body := p.prs[0]["body"].(string); strings.Contains(body, testSecret) {
		t.Fatalf("secret reached the PR body:\n%s", body)
	}
}

// A checkout always carries the repository's own workflow files. Those are the
// base's bytes, not a change, so they must not stop a run that changed nothing
// under .github. Only a change to them is refused.
func TestDiffWorkspaceAllowsAnUnchangedWorkflowFile(t *testing.T) {
	base, ws := t.TempDir(), t.TempDir()
	for _, dir := range []string{base, ws} {
		if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changes, err := diffWorkspace(ws, base, nil)
	if err != nil {
		t.Fatalf("unchanged workflow file refused: %v", err)
	}
	if len(changes) != 1 || changes[0].path != "README.md" {
		t.Fatalf("changes = %+v, want only README.md", changes)
	}
}

// Deleting a workflow file is a change to CI too, and is refused the same way.
func TestDiffWorkspaceRefusesDeletingAWorkflowFile(t *testing.T) {
	base, ws := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".github", "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := diffWorkspace(ws, base, nil); err == nil || !strings.Contains(err.Error(), ".github") {
		t.Fatalf("deleted workflow file: err = %v, want a refusal", err)
	}
}

// docs/CLAUDE.md is a symlink committed to the repository. A checkout always has
// it, so an unchanged link must not stop a run, the same as an unchanged CI file.
func TestDiffWorkspaceAllowsAnUnchangedSymlink(t *testing.T) {
	base, ws := t.TempDir(), t.TempDir()
	for _, dir := range []string{base, ws} {
		if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("AGENTS.md", filepath.Join(dir, "docs", "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := diffWorkspace(ws, base, nil)
	if err != nil || len(changes) != 0 {
		t.Fatalf("unchanged symlink: changes = %+v err = %v", changes, err)
	}
}

// A new symlink that stays inside the repository is a change, recorded as a
// link whose content is its target text. Nothing reads through it.
func TestDiffWorkspaceRecordsANewRelativeSymlink(t *testing.T) {
	base, ws := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../AGENTS.md", filepath.Join(ws, "docs", "GUIDE.md")); err != nil {
		t.Fatal(err)
	}
	changes, err := diffWorkspace(ws, base, nil)
	if err != nil || len(changes) != 1 || changes[0].path != "docs/GUIDE.md" || changes[0].mode != fs.ModeSymlink || string(changes[0].content) != "../AGENTS.md" {
		t.Fatalf("new symlink: changes = %+v err = %v", changes, err)
	}
}

// A link that leaves the repository, by an absolute path or by climbing out, is
// refused: committing it would hand the repository a path outside itself.
func TestDiffWorkspaceRefusesAnEscapingSymlink(t *testing.T) {
	for _, target := range []string{"/etc/passwd", "../../outside"} {
		ws := t.TempDir()
		if err := os.Symlink(target, filepath.Join(ws, "link")); err != nil {
			t.Fatal(err)
		}
		if _, err := diffWorkspace(ws, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "outside the repository") {
			t.Fatalf("link to %s: err = %v, want a refusal", target, err)
		}
	}
}

func TestApplyChangesWritesASymlink(t *testing.T) {
	pub := t.TempDir()
	if err := applyChanges(pub, []change{{path: "docs/GUIDE.md", mode: fs.ModeSymlink, content: []byte("../AGENTS.md")}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(pub, "docs", "GUIDE.md"))
	if err != nil || got != "../AGENTS.md" {
		t.Fatalf("link = %q err = %v", got, err)
	}
}

// A new in-repository link is committed as a link, so the repository keeps the
// relationship rather than a copy of the target's text.
func TestPublishCommitsANewSymlinkAsALink(t *testing.T) {
	p := newPubEnv(t)
	run := p.newRun("00000000000000000000000000000010")
	if err := os.Symlink("README.md", filepath.Join(p.ws(run.ID), "GUIDE.md")); err != nil {
		t.Fatal(err)
	}
	p.publish(run, passingGoTest)
	if got := p.state(run.ID); got.State != ports.AgentRunCompleted {
		t.Fatalf("run = %+v", got)
	}
	tree := gitT(t, p.bare, "ls-tree", "eggy/"+run.ID, "GUIDE.md")
	if !strings.HasPrefix(tree, "120000 ") {
		t.Fatalf("GUIDE.md tree entry = %q, want a symlink (mode 120000)", tree)
	}
}
