package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// fakeAgentRunner records the account-scoped calls and answers with fixed data.
type fakeAgentRunner struct {
	runs    map[string]ports.AgentRun
	steps   map[string][]ports.AgentRunEvent
	started []string
}

const fakeToken = "ghp_must_never_leave_the_server"

func (f *fakeAgentRunner) Runs(context.Context) ([]ports.AgentRun, error) {
	var out []ports.AgentRun
	for _, run := range f.runs {
		out = append(out, run)
	}
	return out, nil
}
func (f *fakeAgentRunner) StartRun(_ context.Context, repository, task string) (ports.AgentRun, error) {
	f.started = append(f.started, repository+"|"+task)
	return ports.AgentRun{ID: "r1", Repository: repository, State: ports.AgentRunQueued, Brief: task, AccountID: "a"}, nil
}
func (f *fakeAgentRunner) Run(_ context.Context, id string) (ports.AgentRun, error) {
	run, ok := f.runs[id]
	if !ok {
		return ports.AgentRun{}, ports.ErrAgentRunNotFound
	}
	return run, nil
}
func (f *fakeAgentRunner) RunSteps(_ context.Context, id string) ([]ports.AgentRunEvent, error) {
	if _, ok := f.runs[id]; !ok {
		return nil, ports.ErrAgentRunNotFound
	}
	return f.steps[id], nil
}
func (f *fakeAgentRunner) CancelRun(ctx context.Context, id string) (ports.AgentRun, error) {
	return f.Run(ctx, id)
}
func (f *fakeAgentRunner) AnswerRun(ctx context.Context, id, _ string) (ports.AgentRun, error) {
	return f.Run(ctx, id)
}
func (f *fakeAgentRunner) Repositories(context.Context) ([]AgentRepository, error) {
	return []AgentRepository{{Name: "eggy", Enabled: true, PushAccess: true}}, nil
}
func (f *fakeAgentRunner) SetRepositoryEnabled(context.Context, string, bool) error { return nil }
func (f *fakeAgentRunner) PAT(context.Context) (AgentPAT, error) {
	return AgentPAT{Connected: true, Login: "nigel", Generation: 1}, nil
}
func (f *fakeAgentRunner) SetPAT(context.Context, string) (AgentPAT, error) {
	// The fake, like the real service, returns the verified status, never the token.
	return AgentPAT{Connected: true, Login: "nigel", Generation: 2}, nil
}
func (f *fakeAgentRunner) DisconnectPAT(context.Context) error { return nil }
func (f *fakeAgentRunner) SandboxStatus() (bool, string)       { return false, "not root" }

// serveAgentRunner mounts the routes exactly as the web panel does, behind a
// pass-through guard, and returns the mux.
func serveAgentRunner(svc AgentRunnerService) *http.ServeMux {
	mux := http.NewServeMux()
	mountAgentRunnerRoutes(mux, func(next http.HandlerFunc) http.Handler { return next }, "/nonexistent/config.yaml", svc, nil, nil)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAgentRunnerNeverReturnsTheToken(t *testing.T) {
	mux := serveAgentRunner(&fakeAgentRunner{})
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/agent-runner/pat", ""},
		{http.MethodPut, "/api/agent-runner/pat", `{"token":"` + fakeToken + `"}`},
		{http.MethodDelete, "/api/agent-runner/pat", ""},
	} {
		rec := do(t, mux, call.method, call.path, call.body)
		if strings.Contains(rec.Body.String(), fakeToken) {
			t.Fatalf("%s %s returned the token: %s", call.method, call.path, rec.Body.String())
		}
	}
}

func TestAgentRunStartRejectsUnknownFields(t *testing.T) {
	fake := &fakeAgentRunner{}
	mux := serveAgentRunner(fake)
	rec := do(t, mux, http.MethodPost, "/api/agent-runner/runs", `{"repository":"eggy","task":"x","account_id":"someone-else"}`)
	if rec.Code != http.StatusBadRequest || len(fake.started) != 0 {
		t.Fatalf("status %d, started %v; a body naming an account must be refused", rec.Code, fake.started)
	}
}

func TestAgentRunForeignRunIs404(t *testing.T) {
	mux := serveAgentRunner(&fakeAgentRunner{runs: map[string]ports.AgentRun{}})
	if rec := do(t, mux, http.MethodGet, "/api/agent-runner/runs/not-mine", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// The browser's view of a run carries the owner's brief, so the run page can
// show the task asked for, but no account: the account is nothing the page needs.
func TestAgentRunViewCarriesBriefButNotAccount(t *testing.T) {
	fake := &fakeAgentRunner{runs: map[string]ports.AgentRun{"r1": {ID: "r1", Repository: "eggy", State: ports.AgentRunRunning, Brief: "fix the docs", AccountID: "a"}}}
	rec := do(t, serveAgentRunner(fake), http.MethodGet, "/api/agent-runner/runs/r1", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "fix the docs") || strings.Contains(body, "account") {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
}

func TestAgentRunnerRoutesMountOnlyWithService(t *testing.T) {
	mux := serveAgentRunner(&fakeAgentRunner{})
	if rec := do(t, mux, http.MethodGet, "/api/agent-runner/repos", ""); rec.Code != http.StatusOK {
		t.Fatalf("repos status = %d", rec.Code)
	}
	// A check whose config file cannot be written is refused, not silently kept.
	rec := do(t, mux, http.MethodPost, "/api/config/repositories/eggy/checks", `{"checks":[["go","test"]]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("checks status = %d, want 400 for a missing config", rec.Code)
	}
}

func TestAgentSkillPairsRoundTrip(t *testing.T) {
	pairs := agentSkillPairs(map[string]bool{"brainstorming": false})
	if !strings.Contains(pairs, "brainstorming=false") || !strings.Contains(pairs, "writing-plans=true") {
		t.Fatalf("pairs = %s", pairs)
	}
	back := parseAgentSkillPairs(pairs)
	if back["brainstorming"] || !back["writing-plans"] {
		t.Fatalf("parsed = %v", back)
	}
}

// The session reports the agent runner only when it is wired, and the public
// mode probe never reports it at all: whether a deployment runs coding runs is
// not for an anonymous caller to learn.
func TestSessionReportsAgentRunnerOnlyWhenWired(t *testing.T) {
	for _, c := range []struct {
		name   string
		runner AgentRunnerService
		want   []string
	}{{"disabled", nil, []string{}}, {"enabled", &fakeAgentRunner{}, []string{"agent_runner"}}} {
		webConfig := testWebConfig(t, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
		if c.runner != nil {
			webConfig.AgentRunner = c.runner
		}
		handler := NewWebHandler("", webConfig)
		cookie := webLoginCookie(t, handler)
		response := call(t, handler, cookie, http.MethodGet, "/api/session", "")
		var body struct {
			Features []string `json:"features"`
		}
		decode(t, response, &body)
		if strings.Join(body.Features, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%s: features = %v, want %v", c.name, body.Features, c.want)
		}
		mode := httptest.NewRecorder()
		handler.ServeHTTP(mode, httptest.NewRequest(http.MethodGet, "/api/mode", nil))
		if strings.Contains(mode.Body.String(), "agent_runner") {
			t.Fatalf("%s: /api/mode reports the agent runner: %s", c.name, mode.Body.String())
		}
	}
}

// Disabled, the routes do not exist at all, so a request is a plain 404 with no
// handler behind it.
func TestAgentRunnerRoutesAbsentWhenNil(t *testing.T) {
	webConfig := testWebConfig(t, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
	handler := NewWebHandler("", webConfig)
	cookie := webLoginCookie(t, handler)
	if response := call(t, handler, cookie, http.MethodGet, "/api/agent-runner/runs", ""); response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with no agent runner", response.Code)
	}
}

// The page learns whether bash is available and, when it is not, why.
func TestAgentRunnerStatusReportsSandbox(t *testing.T) {
	rec := do(t, serveAgentRunner(&fakeAgentRunner{}), http.MethodGet, "/api/agent-runner/status", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sandbox_ok":false`) || !strings.Contains(rec.Body.String(), "not root") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

// Adding a repository goes through the config authority: an unsafe URL is a 400
// and the config file is not touched.
func TestAgentRepoAddRefusesUnsafeURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mux := http.NewServeMux()
	mountAgentRunnerRoutes(mux, func(next http.HandlerFunc) http.Handler { return next }, path, &fakeAgentRunner{}, nil, nil)
	rec := do(t, mux, http.MethodPost, "/api/agent-runner/repositories", `{"name":"x","clone_url":"git@github.com:a/x.git"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rec = do(t, mux, http.MethodPost, "/api/agent-runner/repositories", `{"name":"x","clone_url":"https://github.com/a/x.git","owner":"me"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", rec.Code)
	}
}

// A repository saved a moment ago is shown at once and marked as waiting for a
// restart; one the config dropped is still shown, marked removed; edited checks
// also ask for a restart; an untouched repository asks for nothing.
func TestMergeRepositoriesMarksWhatNeedsARestart(t *testing.T) {
	saved := []config.RepositoryConfig{
		{Name: "steady", Checks: [][]string{{"go", "test"}}},
		{Name: "edited", Checks: [][]string{{"go", "vet"}}},
		{Name: "new"},
	}
	live := []AgentRepository{
		{Name: "steady", Enabled: true, PushAccess: true, Checks: [][]string{{"go", "test"}}},
		{Name: "edited", Enabled: true, Checks: [][]string{{"go", "test"}}},
		{Name: "gone", Enabled: true},
	}
	got := map[string]AgentRepository{}
	for _, repo := range mergeRepositories(saved, live) {
		got[repo.Name] = repo
	}
	if got["steady"].NeedsRestart || !got["steady"].Enabled {
		t.Fatalf("steady = %+v", got["steady"])
	}
	if !got["edited"].NeedsRestart || got["edited"].Checks[0][1] != "vet" {
		t.Fatalf("edited = %+v", got["edited"])
	}
	if !got["new"].Pending || !got["new"].NeedsRestart || got["new"].Enabled {
		t.Fatalf("new = %+v", got["new"])
	}
	if !got["gone"].Removed || !got["gone"].NeedsRestart {
		t.Fatalf("gone = %+v", got["gone"])
	}
}

// Adding a repository saves it and restarts Eggy once, so the runner reads the
// new list; a refused add restarts nothing.
func TestAgentRepoAddRestartsOnlyAfterASuccessfulWrite(t *testing.T) {
	restarter := &fakeRestarter{}
	path := writeConfigFile(t, validConfig())
	mux := http.NewServeMux()
	mountAgentRunnerRoutes(mux, func(next http.HandlerFunc) http.Handler { return next }, path, &fakeAgentRunner{}, restarter, restartEnv)

	bad := do(t, mux, http.MethodPost, "/api/agent-runner/repositories", `{"name":"x","clone_url":"git@github.com:a/x.git"}`)
	if bad.Code != http.StatusBadRequest || restarter.restarts != 0 {
		t.Fatalf("refused add: status %d, restarts %d", bad.Code, restarter.restarts)
	}
	good := do(t, mux, http.MethodPost, "/api/agent-runner/repositories", `{"name":"widgets","clone_url":"https://github.com/acme/widgets.git"}`)
	if good.Code != http.StatusOK || restarter.restarts != 1 || !strings.Contains(good.Body.String(), "restarting") {
		t.Fatalf("add: status %d, restarts %d, body %s", good.Code, restarter.restarts, good.Body.String())
	}
	gone := do(t, mux, http.MethodDelete, "/api/agent-runner/repositories/widgets", "")
	if gone.Code != http.StatusOK || restarter.restarts != 2 {
		t.Fatalf("remove: status %d, restarts %d", gone.Code, restarter.restarts)
	}
}

// A run's steps are served as their own list: in order, never null for a run
// with none, and a run that is not the owner's reads as not found.
func TestAgentRunStepsServeTheRecord(t *testing.T) {
	fake := &fakeAgentRunner{
		runs:  map[string]ports.AgentRun{"r1": {ID: "r1", Repository: "eggy", State: ports.AgentRunFailed}, "r2": {ID: "r2", Repository: "eggy", State: ports.AgentRunQueued}},
		steps: map[string][]ports.AgentRunEvent{"r1": {{Seq: 1, Kind: "started", Text: "coding run started"}, {Seq: 2, Kind: "failed", Text: "check failed"}}},
	}
	mux := serveAgentRunner(fake)
	rec := do(t, mux, http.MethodGet, "/api/agent-runner/runs/r1/steps", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"failed"`) || !strings.Contains(rec.Body.String(), "check failed") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	empty := do(t, mux, http.MethodGet, "/api/agent-runner/runs/r2/steps", "")
	if strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("run with no steps body = %q, want []", empty.Body.String())
	}
	if rec := do(t, mux, http.MethodGet, "/api/agent-runner/runs/not-mine/steps", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign run status = %d, want 404", rec.Code)
	}
}

// The run page shows what a run spent and where it went, so the browser view
// carries the usage and the branch names. The brief stays out.
func TestAgentRunViewCarriesUsageAndBranches(t *testing.T) {
	fake := &fakeAgentRunner{runs: map[string]ports.AgentRun{"r1": {
		ID: "r1", Repository: "eggy", State: ports.AgentRunCompleted, BaseBranch: "main", Branch: "eggy/r1",
		UsedTokens: 150000, UsedActive: 95 * time.Second, PRTitle: "Paginate finance entries",
	}}}
	rec := do(t, serveAgentRunner(fake), http.MethodGet, "/api/agent-runner/runs/r1", "")
	var got agentRunJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BaseBranch != "main" || got.Branch != "eggy/r1" || got.UsedTokens != 150000 || got.ActiveSeconds != 95 || got.PRTitle != "Paginate finance entries" {
		t.Fatalf("view = %+v", got)
	}
}
