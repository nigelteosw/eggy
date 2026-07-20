package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodingServiceRunsImplementerCapturesDiffAndPersistsResult(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}
	repository := &fakeRepository{}
	implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", Validation: "tests pass", CommitMessage: "feat: done"}}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewCodingService(store, runner, repository, implementer, func() time.Time { return now }, nil)
	var updates []ports.CodingProgress
	run, result, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", func(progress ports.CodingProgress) { updates = append(updates, progress) })
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || run.Diff != "diff" || run.Branch != "eggy/run-1" || run.BaseRevision != "abc123" || result.CommitMessage != "feat: done" {
		t.Fatalf("run=%#v result=%#v", run, result)
	}
	if !runner.created || repository.clones != 1 || repository.branches != 1 || implementer.runID != "run-1" || implementer.workspace != runner.workspace || !strings.Contains(implementer.instruction, "Do not create, switch, rename, or delete branches") || !strings.Contains(implementer.instruction, "Do not commit, push, or create pull requests") {
		t.Fatalf("runner=%#v repository=%#v implementer=%#v", runner, repository, implementer)
	}
	state, _ := store.Load(context.Background())
	if state.CodingRuns["run-1"].Status != "completed" {
		t.Fatalf("state=%#v", state.CodingRuns)
	}
	var checkpoints []string
	for _, update := range updates {
		if update.Kind == "checkpoint" {
			checkpoints = append(checkpoints, update.Message)
		}
	}
	wantCheckpoints := []string{
		"Preparing an isolated workspace for eggy",
		"Cloning eggy@main",
		"Creating branch eggy/run-1",
		"Starting the implementation run",
		"Capturing diff and validation evidence",
	}
	if strings.Join(checkpoints, "|") != strings.Join(wantCheckpoints, "|") {
		t.Fatalf("checkpoints = %#v, want %#v", checkpoints, wantCheckpoints)
	}
}

func TestCodingServiceRejectsBranchOrHeadChangesBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeRepository)
		want   string
	}{
		{name: "branch", mutate: func(repository *fakeRepository) { repository.branch = "feat/unapproved" }, want: "branch"},
		{name: "head", mutate: func(repository *fakeRepository) { repository.head = "unapproved-commit" }, want: "HEAD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemoryStore()
			store.state.CodingRuns = map[string]ports.CodingRun{}
			repository := &fakeRepository{}
			implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", CommitMessage: "feat: done"}, onRun: func() { test.mutate(repository) }}
			service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}, repository, implementer, time.Now, nil)

			_, _, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
			state, _ := store.Load(context.Background())
			if state.CodingRuns["run-1"].Status != "failed" {
				t.Fatalf("run=%#v", state.CodingRuns["run-1"])
			}
		})
	}
}

func TestCodingServiceRecoversInterruptedRunsAndCleansWorkspace(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Workspace: "/tmp/runs/run", Status: "running"}}
	runner := &fakeWorkspaceRunner{}
	service := NewCodingService(store, runner, &fakeRepository{}, &fakeImplementer{}, time.Now, nil)
	count, err := service.RecoverInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	state, _ := store.Load(context.Background())
	if state.CodingRuns["run"].Status != "interrupted" {
		t.Fatalf("run=%#v", state.CodingRuns["run"])
	}
	if err := service.Cleanup(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if !runner.destroyed {
		t.Fatal("workspace not destroyed")
	}
	state, _ = store.Load(context.Background())
	if state.CodingRuns["run"].Workspace != "" {
		t.Fatalf("workspace retained in state: %#v", state.CodingRuns["run"])
	}
}

func TestCodingServiceResumeUsesPersistedWorkspaceAndContext(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run-1": {
		ID: "run-1", Repository: "eggy", Workspace: "/data/runs/run-1", Branch: "eggy/run-1", BaseRevision: "abc123", Status: "interrupted",
	}}
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessionStore := newMemorySessionStore()
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{ContextBudgetChars: 1000, RecentMessages: 4, OutputExcerptChars: 200}, time.Now)
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: "/data/runs/run-1", Branch: "eggy/run-1", BaseRevision: "abc123", Instruction: "add sessions", Status: ports.SessionInterrupted, Context: ports.SessionContext{Summary: "Inspected: README.md"}}); err != nil {
		t.Fatal(err)
	}
	implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", CommitMessage: "feat: resume"}}
	repository := &fakeRepository{branch: "eggy/run-1", head: "abc123"}
	service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/data/runs/run-1"}, repository, implementer, time.Now, sessions)

	run, _, err := service.Resume(context.Background(), "run-1", "fix the test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "run-1" || repository.clones != 0 || repository.branches != 0 || implementer.workspace != "/data/runs/run-1" {
		t.Fatalf("run=%#v repository=%#v implementer=%#v", run, repository, implementer)
	}
	if len(implementer.history) == 0 || !strings.Contains(implementer.history[0].Content, "Previous implementation session") {
		t.Fatalf("history=%#v", implementer.history)
	}
}

type fakeWorkspaceRunner struct {
	workspace          string
	created, destroyed bool
}

func (r *fakeWorkspaceRunner) Create(context.Context, string) (string, error) {
	r.created = true
	return r.workspace, nil
}
func (r *fakeWorkspaceRunner) Execute(context.Context, ports.Command) (ports.CommandResult, error) {
	return ports.CommandResult{}, nil
}
func (r *fakeWorkspaceRunner) Destroy(context.Context, string) error { r.destroyed = true; return nil }

type fakeImplementer struct {
	runID, workspace, instruction string
	history                       []ports.Message
	result                        ports.CodingResult
	onRun                         func()
}

func (a *fakeImplementer) Implement(_ context.Context, request ImplementationRequest, _ func(ports.ImplementationSessionEvent), progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	a.runID, a.workspace, a.instruction, a.history = request.RunID, request.Workspace, request.Instruction, request.History
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "working"})
	}
	if a.onRun != nil {
		a.onRun()
	}
	return a.result, nil
}
func (a *fakeImplementer) Interrupt(string) error { return nil }
