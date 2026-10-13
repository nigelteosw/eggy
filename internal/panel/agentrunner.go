package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// AgentRunnerService is the agent runner as the panel sees it. The panel
// declares the interface and never imports the extension: bootstrap adapts the
// service, so the panel and the /code command share one path to every run.
//
// Every method acts as the account on the context, which the session guard put
// there. No route reads an account from a body, a query or a path.
type AgentRunnerService interface {
	Runs(ctx context.Context) ([]ports.AgentRun, error)
	StartRun(ctx context.Context, repository, task string) (ports.AgentRun, error)
	Run(ctx context.Context, id string) (ports.AgentRun, error)
	RunSteps(ctx context.Context, id string) ([]ports.AgentRunEvent, error)
	CancelRun(ctx context.Context, id string) (ports.AgentRun, error)
	AnswerRun(ctx context.Context, id, text string) (ports.AgentRun, error)
	Repositories(ctx context.Context) ([]AgentRepository, error)
	SetRepositoryEnabled(ctx context.Context, name string, enabled bool) error
	PAT(ctx context.Context) (AgentPAT, error)
	SetPAT(ctx context.Context, token string) (AgentPAT, error)
	DisconnectPAT(ctx context.Context) error
	SandboxStatus() (bool, string)
}

// AgentRepository is one allowed repository as an account sees it.
type AgentRepository struct {
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	PushAccess bool       `json:"push_access"`
	Checks     [][]string `json:"checks"`
	// Pending is a saved repository the running process has not loaded yet.
	// Removed is one the process still runs that the config no longer lists.
	// NeedsRestart is true for either, and for checks that were edited since
	// startup: all three change only when Eggy restarts.
	Pending      bool `json:"pending"`
	Removed      bool `json:"removed"`
	NeedsRestart bool `json:"needs_restart"`
}

// AgentPAT is what the browser may learn about an account's GitHub token. The
// token itself is not in this type, so no route can return it.
type AgentPAT struct {
	Connected    bool            `json:"connected"`
	Login        string          `json:"login,omitempty"`
	Generation   int64           `json:"generation"`
	Repositories map[string]bool `json:"repositories,omitempty"`
}

// agentRunJSON is a run as the browser sees it. It carries no brief, no
// origin and no account: the brief is the owner's own text, shown from the
// run's page, and the rest is nothing the browser needs.
type agentRunJSON struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	State      string `json:"state"`
	Stage      string `json:"stage,omitempty"`
	Question   string `json:"question,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	Draft      bool   `json:"draft"`
	Failure    string `json:"failure,omitempty"`
	Output     string `json:"output,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func agentRunView(run ports.AgentRun) agentRunJSON {
	return agentRunJSON{
		ID: run.ID, Repository: run.Repository, State: string(run.State), Stage: string(run.Stage),
		Question: run.Question, PRURL: run.PRURL, Draft: run.Draft, Failure: run.Failure,
		Output: run.Output, CreatedAt: run.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// writeAgentFailure says whose fault a refusal was, as the finance routes do: a
// refused request is a 400 with its reason, a run that is not the caller's is
// a 404, and anything else is a 500 that does not repeat the error.
func writeAgentFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrAgentRunNotFound):
		writeWebError(w, http.StatusNotFound, "run not found")
	case errors.Is(err, ports.ErrNoPrincipal):
		writeWebError(w, http.StatusUnauthorized, "sign in to use the agent runner")
	default:
		writeWebError(w, http.StatusBadRequest, err.Error())
	}
}

// decodeAgentBody refuses any field it does not know. That is what keeps an
// account, an owner or a destination from being smuggled into a request body.
func decodeAgentBody(r *http.Request, into any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

// mountAgentRunnerRoutes registers the agent runner's API. It is called only
// when the service exists, so a disabled extension has no route at all.
func mountAgentRunnerRoutes(mux *http.ServeMux, guard func(http.HandlerFunc) http.Handler, configPath string, svc AgentRunnerService, restarter commands.Restarter, getenv func(string) string) {
	mux.Handle("GET /api/agent-runner/status", guard(agentStatus(svc)))
	mux.Handle("GET /api/agent-runner/runs", guard(agentRunsList(svc)))
	mux.Handle("POST /api/agent-runner/runs", guard(agentRunStart(svc)))
	mux.Handle("GET /api/agent-runner/runs/{id}", guard(agentRunGet(svc)))
	mux.Handle("GET /api/agent-runner/runs/{id}/steps", guard(agentRunSteps(svc)))
	mux.Handle("POST /api/agent-runner/runs/{id}/cancel", guard(agentRunCancel(svc)))
	mux.Handle("POST /api/agent-runner/runs/{id}/answer", guard(agentRunAnswer(svc)))
	mux.Handle("GET /api/agent-runner/repos", guard(agentRepos(svc, configPath)))
	mux.Handle("POST /api/agent-runner/repos/{name}", guard(agentRepoToggle(svc, configPath)))
	mux.Handle("GET /api/agent-runner/pat", guard(agentPATGet(svc)))
	mux.Handle("PUT /api/agent-runner/pat", guard(agentPATSet(svc)))
	mux.Handle("DELETE /api/agent-runner/pat", guard(agentPATDelete(svc)))
	mux.Handle("POST /api/agent-runner/repositories", guard(agentRepoAdd(configPath, restarter, getenv)))
	mux.Handle("DELETE /api/agent-runner/repositories/{name}", guard(agentRepoRemove(configPath, restarter, getenv)))
	mux.Handle("POST /api/config/repositories/{name}/checks", guard(agentChecksSet(configPath)))
}

// agentStatus says whether bash is offered, and why not when it is not, so the
// page can tell the owner what the runs will be able to do.
func agentStatus(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ok, reason := svc.SandboxStatus()
		writeJSON(w, map[string]any{"sandbox_ok": ok, "sandbox_reason": reason})
	}
}

func agentRunsList(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runs, err := svc.Runs(r.Context())
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		views := make([]agentRunJSON, 0, len(runs))
		for _, run := range runs {
			views = append(views, agentRunView(run))
		}
		writeJSON(w, views)
	}
}

func agentRunStart(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Repository string `json:"repository"`
			Task       string `json:"task"`
		}
		if err := decodeAgentBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		run, err := svc.StartRun(r.Context(), body.Repository, body.Task)
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, agentRunView(run))
	}
}

// agentRunSteps serves a run's record, oldest step first. An empty run has no
// steps and reads as an empty list, never null.
func agentRunSteps(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		steps, err := svc.RunSteps(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		if steps == nil {
			steps = []ports.AgentRunEvent{}
		}
		writeJSON(w, steps)
	}
}

func agentRunGet(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := svc.Run(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, agentRunView(run))
	}
}

func agentRunCancel(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := svc.CancelRun(r.Context(), r.PathValue("id"))
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, agentRunView(run))
	}
}

func agentRunAnswer(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := decodeAgentBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		run, err := svc.AnswerRun(r.Context(), r.PathValue("id"), body.Text)
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, agentRunView(run))
	}
}

// savedRepositories merges what the running service knows with what the config
// file says, so a repository added a moment ago is shown at once and marked as
// waiting for a restart, instead of being invisible until one happens.
func savedRepositories(ctx context.Context, svc AgentRunnerService, configPath string) ([]AgentRepository, error) {
	live, err := svc.Repositories(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		// No readable file, such as in a test: the running list is all there is.
		return live, nil
	}
	return mergeRepositories(cfg.Repositories, live), nil
}

// mergeRepositories keeps the config's order and checks, and takes enablement
// and push access from the running service where it knows the repository.
func mergeRepositories(saved []config.RepositoryConfig, live []AgentRepository) []AgentRepository {
	liveByName := make(map[string]AgentRepository, len(live))
	for _, repo := range live {
		liveByName[repo.Name] = repo
	}
	merged := make([]AgentRepository, 0, len(saved))
	seen := map[string]bool{}
	for _, repo := range saved {
		seen[repo.Name] = true
		running, known := liveByName[repo.Name]
		if !known {
			merged = append(merged, AgentRepository{Name: repo.Name, Checks: repo.Checks, Pending: true, NeedsRestart: true})
			continue
		}
		running.NeedsRestart = !reflect.DeepEqual(nonNil(running.Checks), nonNil(repo.Checks))
		running.Checks = repo.Checks
		merged = append(merged, running)
	}
	for _, repo := range live {
		if !seen[repo.Name] {
			repo.Removed, repo.NeedsRestart = true, true
			merged = append(merged, repo)
		}
	}
	return merged
}

// nonNil makes a nil and an empty check list compare equal.
func nonNil(checks [][]string) [][]string {
	if checks == nil {
		return [][]string{}
	}
	return checks
}

func agentRepos(svc AgentRunnerService, configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repos, err := savedRepositories(r.Context(), svc, configPath)
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, repos)
	}
}

func agentRepoToggle(svc AgentRunnerService, configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeAgentBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := svc.SetRepositoryEnabled(r.Context(), r.PathValue("name"), body.Enabled); err != nil {
			writeAgentFailure(w, err)
			return
		}
		repos, err := savedRepositories(r.Context(), svc, configPath)
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, repos)
	}
}

func agentPATGet(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := svc.PAT(r.Context())
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, status)
	}
}

// agentPATSet verifies a new token before it is stored. The response is the
// verified status, which never includes the token, so a failed verification
// cannot echo it back either: the error text comes from GitHub, not the input.
func agentPATSet(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if err := decodeAgentBody(r, &body); err != nil || strings.TrimSpace(body.Token) == "" {
			writeWebError(w, http.StatusBadRequest, "a GitHub token is required")
			return
		}
		status, err := svc.SetPAT(r.Context(), strings.TrimSpace(body.Token))
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, status)
	}
}

func agentPATDelete(svc AgentRunnerService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := svc.DisconnectPAT(r.Context()); err != nil {
			writeAgentFailure(w, err)
			return
		}
		status, err := svc.PAT(r.Context())
		if err != nil {
			writeAgentFailure(w, err)
			return
		}
		writeJSON(w, status)
	}
}

// agentChecksSet replaces one allowed repository's check list. It writes through
// the config authority, so the same validation every other config write gets
// applies: an empty argv or an unknown repository is refused.
func agentChecksSet(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Checks [][]string `json:"checks"`
		}
		if err := decodeAgentBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := config.SetRepositoryChecks(configPath, r.PathValue("name"), body.Checks); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved checks."})
	}
}

// restartAfterRepositoryChange restarts Eggy so the runner reads the new
// repository list, through the one restart path: its pre-flight refuses a
// config that would not start, and then the change is saved but not applied,
// which the owner is told.
func restartAfterRepositoryChange(title string, restarter commands.Restarter, configPath string, getenv func(string) string) webResult {
	message, restarting := commands.Restart(restarter, configPath, getenv)
	if restarting {
		return webResult{State: webSuccess, Title: title, Detail: "Eggy is restarting to apply it. Reload in a few seconds."}
	}
	return webResult{State: webInfo, Title: title, Detail: "Saved, but not applied: " + message}
}

// agentRepoAdd allows a repository. It writes through the config authority, so
// the same URL and name rules apply as to a hand-edited file, and then restarts
// Eggy so the runner reads the new list. A change that could not be written
// restarts nothing.
func agentRepoAdd(configPath string, restarter commands.Restarter, getenv func(string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name       string `json:"name"`
			CloneURL   string `json:"clone_url"`
			BaseBranch string `json:"base_branch"`
		}
		if err := decodeAgentBody(r, &body); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		repo := config.RepositoryConfig{Name: body.Name, CloneURL: body.CloneURL, BaseBranch: body.BaseBranch}
		if err := config.AddRepository(configPath, repo); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, restartAfterRepositoryChange("Added "+body.Name+".", restarter, configPath, getenv))
	}
}

// agentRepoRemove stops allowing a repository and restarts Eggy to apply it.
// The run records and any workspace are kept.
func agentRepoRemove(configPath string, restarter commands.Restarter, getenv func(string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := config.RemoveRepository(configPath, name); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, restartAfterRepositoryChange("Removed "+name+".", restarter, configPath, getenv))
	}
}
