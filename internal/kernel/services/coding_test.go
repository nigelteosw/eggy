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
	sessionStore := newMemorySessionStore()
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}
	repository := &fakeRepository{}
	implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", Validation: "tests pass", CommitMessage: "feat: done"}}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewCodingService(store, runner, repository, implementer, func() time.Time { return now }, sessions)
	var updates []ports.CodingProgress
	run, result, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", func(progress ports.CodingProgress) { updates = append(updates, progress) })
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != ports.PhaseReady || run.Diff != "diff" || run.Branch != "eggy/run-1" || run.BaseRevision != "abc123" || result.CommitMessage != "feat: done" {
		t.Fatalf("run=%#v result=%#v", run, result)
	}
	if !runner.created || repository.clones != 1 || repository.branches != 1 || implementer.runID != "run-1" || implementer.workspace != runner.workspace || !strings.Contains(implementer.instruction, "Do not create, switch, rename, or delete branches") || !strings.Contains(implementer.instruction, "Do not commit, push, or create pull requests") {
		t.Fatalf("runner=%#v repository=%#v implementer=%#v", runner, repository, implementer)
	}
	persisted, err := sessions.Load(context.Background(), "run-1")
	if err != nil || persisted.Phase != ports.PhaseReady {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
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
			sessions := NewImplementationSessions(newMemorySessionStore(), SessionPolicy{}, time.Now)
			repository := &fakeRepository{}
			implementer := &fakeImplementer{result: ports.CodingResult{Summary: "done", CommitMessage: "feat: done"}, onRun: func() { test.mutate(repository) }}
			service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}, repository, implementer, time.Now, sessions)

			_, _, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
			persisted, err := sessions.Load(context.Background(), "run-1")
			if err != nil || persisted.Phase != ports.PhaseBlocked {
				t.Fatalf("persisted=%#v err=%v", persisted, err)
			}
		})
	}
}

func TestCodingServiceRecoversInterruptedRunsAndCleansWorkspace(t *testing.T) {
	store := newMemoryStore()
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run"] = ports.ImplementationSession{ID: "run", Workspace: "/tmp/runs/run", Phase: ports.PhaseRunning}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	runner := &fakeWorkspaceRunner{}
	service := NewCodingService(store, runner, &fakeRepository{}, &fakeImplementer{}, time.Now, sessions)
	count, err := service.RecoverInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	persisted, err := sessions.Load(context.Background(), "run")
	if err != nil || persisted.Phase != ports.PhaseInterrupted {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
	if err := service.Cleanup(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if !runner.destroyed {
		t.Fatal("workspace not destroyed")
	}
	persisted, err = sessions.Load(context.Background(), "run")
	if err != nil || persisted.Workspace != "" {
		t.Fatalf("workspace retained in session: %#v", persisted)
	}
}

func TestCodingServiceResumeUsesPersistedWorkspaceAndContext(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessionStore := newMemorySessionStore()
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{ContextBudgetChars: 1000, RecentMessages: 4, OutputExcerptChars: 200}, time.Now)
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: "/data/runs/run-1", Branch: "eggy/run-1", BaseRevision: "abc123", Instruction: "add sessions", Phase: ports.PhaseInterrupted, Context: ports.SessionContext{Summary: "Inspected: README.md"}}); err != nil {
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

func TestCodingServiceResumeInvalidatesPendingCommitApproval(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessionStore := newMemorySessionStore()
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: "/data/runs/run-1", Branch: "eggy/run-1", BaseRevision: "abc123", Phase: ports.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	invalidator := &fakePendingCommitInvalidator{}
	service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/data/runs/run-1"}, &fakeRepository{branch: "eggy/run-1", head: "abc123"}, &fakeImplementer{result: ports.CodingResult{CommitMessage: "feat: resume"}}, time.Now, sessions, invalidator)
	if _, _, err := service.Resume(context.Background(), "run-1", "continue", nil); err != nil {
		t.Fatal(err)
	}
	if invalidator.runID != "run-1" {
		t.Fatalf("invalidator=%#v", invalidator)
	}
}

func TestCodingServiceResumeBlocksWhenWorkspaceMissing(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessionStore := newMemorySessionStore()
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	if _, err := sessions.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: "/data/runs/run-1", Branch: "eggy/run-1", BaseRevision: "abc123", Phase: ports.PhaseBlocked}); err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{branch: "different-branch", head: "different-head"}
	service := NewCodingService(store, &fakeWorkspaceRunner{workspace: "/data/runs/run-1"}, repository, &fakeImplementer{}, time.Now, sessions)
	if _, _, err := service.Resume(context.Background(), "run-1", "continue", nil); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("error=%v", err)
	}
	persisted, err := sessions.Load(context.Background(), "run-1")
	if err != nil || persisted.Phase != ports.PhaseBlocked {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
}

func TestCodingServiceRequiresSessionsForStartResumeAndResumeLatest(t *testing.T) {
	store := newMemoryStore()
	service := NewCodingService(store, &fakeWorkspaceRunner{}, &fakeRepository{}, &fakeImplementer{}, time.Now, nil)
	if _, _, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy"}, "implement", nil); err == nil {
		t.Fatal("expected Start to require implementation sessions")
	}
	if _, _, err := service.Resume(context.Background(), "run-1", "continue", nil); err == nil {
		t.Fatal("expected Resume to require implementation sessions")
	}
	if _, _, err := service.ResumeLatest(context.Background(), "continue", nil); err == nil {
		t.Fatal("expected ResumeLatest to require implementation sessions")
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

type fakePendingCommitInvalidator struct{ runID string }

func (f *fakePendingCommitInvalidator) InvalidatePendingCommitForRun(_ context.Context, runID string) error {
	f.runID = runID
	return nil
}
