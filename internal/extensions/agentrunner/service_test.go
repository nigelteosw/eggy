package agentrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeModel answers each request with respond. It records every request, so
// tests can see exactly what the coding turn was offered.
type fakeModel struct {
	mu       sync.Mutex
	requests []ports.ModelRequest
	respond  func(n int, req ports.ModelRequest) (ports.ModelResponse, error)
}

func (f *fakeModel) Generate(_ context.Context, req ports.ModelRequest) (ports.ModelResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	n := len(f.requests)
	f.mu.Unlock()
	return f.respond(n, req)
}

type harness struct {
	svc   *Service
	store *memStore
	model *fakeModel
	base  string // the base commit of the local remote a run publishes to
	notes []string
	mu    sync.Mutex
}

// finalAnswer is a model reply with no tool call: the turn ends with it.
func finalAnswer(text string) ports.ModelResponse {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: text}}
}

// newHarness builds a service with one connected PAT and one enabled
// repository for account "a". mut may change the options before New.
func newHarness(t *testing.T, mut func(*Options), respond func(int, ports.ModelRequest) (ports.ModelResponse, error)) *harness {
	t.Helper()
	creds, _ := fakeGitHubForPAT(t, true)
	store := newMemStore()
	h := &harness{store: store, model: &fakeModel{respond: respond}}
	bare, base := newBareRemote(t)
	h.base = base
	seq := 0
	opts := Options{
		Store:        store,
		Credentials:  creds,
		GitHub:       creds.gh,
		Workspaces:   NewWorkspaces(t.TempDir(), &fakeRunner{}),
		Repositories: func() []config.RepositoryConfig { return testRepos },
		Models:       map[string]agent.ModelTarget{"coder": {Model: h.model, ModelID: "fake-model"}},
		Model:        "coder",
		GitURL:       func(config.RepositoryConfig) string { return bare },
		SandboxOK:    true,
		Now:          func() time.Time { return time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC) },
		NewID: func() string {
			seq++
			return fmt.Sprintf("%032x", seq)
		},
		Notify: func(_ context.Context, _ ports.AgentRun, text string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.notes = append(h.notes, text)
			return nil
		},
	}
	if mut != nil {
		mut(&opts)
	}
	svc, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	if _, err := creds.Set(acct("a"), "ghp_secret", testRepos); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRepositoryEnabled(acct("a"), "r", true); err != nil {
		t.Fatal(err)
	}
	return h
}

func startRun(t *testing.T, h *harness, task string) ports.AgentRun {
	t.Helper()
	run, err := h.svc.Start(acct("a"), StartRequest{Repository: "r", Task: task, Channel: "web", Destination: "panel"})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestStartRefusals(t *testing.T) {
	cases := []struct {
		name string
		prep func(t *testing.T, h *harness)
		repo string
		want string
	}{
		{"unknown repository", nil, "nope", "unknown repository"},
		{"not enabled for account", func(t *testing.T, h *harness) {
			_ = h.store.SetRepositoryEnabled(acct("a"), "r", false)
		}, "r", "not enabled"},
		{"queued run already exists", func(t *testing.T, h *harness) {
			startRun(t, h, "first")
		}, "r", "already have a queued run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil, nil)
			if tc.prep != nil {
				tc.prep(t, h)
			}
			_, err := h.svc.Start(acct("a"), StartRequest{Repository: tc.repo, Task: "do it"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStartRefusesWithoutPushAccessOrToken(t *testing.T) {
	creds, _ := fakeGitHubForPAT(t, false)
	store := newMemStore()
	svc, err := New(Options{Store: store, Credentials: creds, Workspaces: NewWorkspaces(t.TempDir(), &fakeRunner{}),
		Repositories: func() []config.RepositoryConfig { return testRepos }, Models: map[string]agent.ModelTarget{"coder": {Model: &fakeModel{}}}, Model: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.SetRepositoryEnabled(acct("a"), "r", true)
	if _, err := svc.Start(acct("a"), StartRequest{Repository: "r", Task: "x"}); err == nil || !strings.Contains(err.Error(), "connect a GitHub token") {
		t.Fatalf("no token: %v", err)
	}
	if _, err := creds.Set(acct("a"), "ghp", testRepos); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(acct("a"), StartRequest{Repository: "r", Task: "x"}); err == nil || !strings.Contains(err.Error(), "no push access") {
		t.Fatalf("no push: %v", err)
	}
}

func TestStartRecordsMandate(t *testing.T) {
	h := newHarness(t, nil, nil)
	run := startRun(t, h, "add a line to README")
	digest := sha256.Sum256([]byte("add a line to README"))
	if run.BriefDigest != hex.EncodeToString(digest[:]) {
		t.Fatalf("digest = %s", run.BriefDigest)
	}
	if run.Branch != "eggy/"+run.ID || run.State != ports.AgentRunQueued || run.AccountID != "a" {
		t.Fatalf("run = %+v", run)
	}
	if want := run.CreatedAt.Add(24 * time.Hour); !run.MandateExpires.Equal(want) {
		t.Fatalf("mandate expires %v, want %v", run.MandateExpires, want)
	}
	if run.PATGeneration != 1 {
		t.Fatalf("generation = %d", run.PATGeneration)
	}
}

func TestForeignRunNotFound(t *testing.T) {
	h := newHarness(t, nil, nil)
	run := startRun(t, h, "x")
	if _, err := h.svc.Get(acct("b"), run.ID); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("foreign get = %v", err)
	}
	if _, err := h.svc.Cancel(acct("b"), run.ID); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("foreign cancel = %v", err)
	}
}

func TestToolHasNoStartAction(t *testing.T) {
	h := newHarness(t, nil, nil)
	def := NewTool(h.svc).Definition()
	if strings.Contains(string(def.Schema), `"start"`) {
		t.Fatalf("agent_runner exposes a start action: %s", def.Schema)
	}
	if !strings.Contains(string(def.Schema), `"enum":["status","list","cancel","answer"]`) {
		t.Fatalf("actions = %s", def.Schema)
	}
}

func TestSandboxStatusReported(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.SandboxOK = false; o.SandboxReason = "landlock missing" }, nil)
	ok, reason := h.svc.SandboxStatus()
	if ok || reason != "landlock missing" {
		t.Fatalf("status = %v %q", ok, reason)
	}
}

func TestAnswerRejectsRunNotWaiting(t *testing.T) {
	h := newHarness(t, nil, nil)
	run := startRun(t, h, "x")
	if _, err := h.svc.Answer(acct("a"), run.ID, "yes"); err == nil {
		t.Fatal("answer accepted for a queued run")
	}
}
