package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestCodingServiceRunsCodexCapturesDiffAndPersistsResult(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/run-1"}
	repository := &fakeRepository{}
	agent := &fakeCodingAgent{result: ports.CodingResult{Summary: "done", Validation: "tests pass", CommitMessage: "feat: done"}}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service := NewCodingService(store, runner, repository, agent, "/data/codex", func() time.Time { return now })
	var updates []ports.CodingProgress
	run, result, err := service.Start(context.Background(), "run-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "implement", func(progress ports.CodingProgress) { updates = append(updates, progress) })
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" || run.Diff != "diff" || run.Branch != "eggy/run-1" || result.CommitMessage != "feat: done" {
		t.Fatalf("run=%#v result=%#v", run, result)
	}
	if !runner.created || repository.clones != 1 || repository.branches != 1 || agent.request.Environment["CODEX_HOME"] != "/data/codex" || agent.request.Workspace != runner.workspace {
		t.Fatalf("runner=%#v repository=%#v request=%#v", runner, repository, agent.request)
	}
	state, _ := store.Load(context.Background())
	if state.CodingRuns["run-1"].Status != "completed" {
		t.Fatalf("state=%#v", state.CodingRuns)
	}
}

func TestCodingServiceRecoversInterruptedRunsAndCleansWorkspace(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{"run": {ID: "run", Workspace: "/tmp/runs/run", Status: "running"}}
	runner := &fakeWorkspaceRunner{}
	service := NewCodingService(store, runner, &fakeRepository{}, &fakeCodingAgent{}, "/data/codex", time.Now)
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

func TestCodingServiceInspectIsReadOnlyEphemeralAndGuided(t *testing.T) {
	store := newMemoryStore()
	store.state.CodingRuns = map[string]ports.CodingRun{}
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/inspect-1"}
	repository := &fakeRepository{guidance: "Follow AGENTS", diff: " "}
	agent := &fakeCodingAgent{result: ports.CodingResult{Summary: "Go standard library"}}
	service := NewCodingService(store, runner, repository, agent, "/data/codex", time.Now)
	result, err := service.Inspect(context.Background(), "inspect-1", ports.Repository{Name: "eggy", BaseBranch: "main"}, "what framework?")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Go standard library" || !agent.request.ReadOnly || !strings.Contains(agent.request.Instruction, "Follow AGENTS") || agent.request.Environment["CODEX_HOME"] != "/data/codex" {
		t.Fatalf("result=%#v request=%#v", result, agent.request)
	}
	if !runner.created || !runner.destroyed || repository.clones != 1 || repository.branches != 0 {
		t.Fatalf("runner=%#v repository=%#v", runner, repository)
	}
	state, _ := store.Load(context.Background())
	if len(state.CodingRuns) != 0 {
		t.Fatalf("inspection persisted runs=%#v", state.CodingRuns)
	}
	repository.diff = "unexpected diff"
	if _, err := service.Inspect(context.Background(), "inspect-2", ports.Repository{Name: "eggy", BaseBranch: "main"}, "inspect"); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("error=%v", err)
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

type fakeCodingAgent struct {
	request ports.CodingRequest
	result  ports.CodingResult
}

func (a *fakeCodingAgent) Run(_ context.Context, request ports.CodingRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	a.request = request
	if progress != nil {
		progress(ports.CodingProgress{Kind: "message", Message: "working"})
	}
	return a.result, nil
}
func (a *fakeCodingAgent) Interrupt(string) error { return nil }
