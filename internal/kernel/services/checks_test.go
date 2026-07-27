package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// checksFixture wires a shipped session whose thread still has the branched
// workspace attached: the only shape a failed check can resume in place.
func checksFixture(t *testing.T, checks []ports.CheckRun) (*ChecksWatcher, *ImplementationSessions) {
	t.Helper()
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", BaseBranch: "main"}}
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run"] = ports.ImplementationSession{
		ID: "run", Repository: "eggy", Workspace: "/tmp/runs/run", Branch: "eggy/run",
		Commit: "abc123", PullRequestURL: "https://example.test/pr/7", PullRequestNumber: 7,
		Phase: ports.PhaseCompleted,
	}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	threads := newFakeThreadStore()
	ctx := destination.With(context.Background(), destination.Destination{Kind: destination.Web, ThreadID: "thread-1"})
	if err := threads.AttachWorkspace(ctx, "thread-1", destination.Web, "eggy", "/tmp/runs/run", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := threads.SetWorkspaceEdit(ctx, "thread-1", "eggy/run", "run"); err != nil {
		t.Fatal(err)
	}
	return NewChecksWatcher(store, sessions, threads, &fakeRepositoryReader{checks: checks}), sessions
}

func TestChecksWatcherResumesTheThreadWhosePullRequestChecksFailed(t *testing.T) {
	watcher, sessions := checksFixture(t, []ports.CheckRun{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "completed", Conclusion: "failure", URL: "https://example.test/checks/2"},
	})

	completions, err := watcher.Poll(context.Background())
	if err != nil || len(completions) != 1 {
		t.Fatalf("completions=%#v err=%v", completions, err)
	}
	completion := completions[0]
	if completion.Session != "run" || completion.Conclusion != "failure" || completion.Ref != "abc123" {
		t.Fatalf("completion=%#v", completion)
	}
	// The turn must land in the thread that proposed the change, because
	// that is the thread whose workspace still holds the branch.
	if completion.Destination.Kind != destination.Web || completion.Destination.ThreadID != "thread-1" {
		t.Fatalf("destination=%#v", completion.Destination)
	}
	if len(completion.Evidence) != 1 || completion.Evidence[0].Name != "test" {
		t.Fatalf("evidence must be the failing checks only: %#v", completion.Evidence)
	}
	instruction := completion.ChecksInstruction()
	for _, want := range []string{"#7", "eggy/run", "test", "https://example.test/checks/2", "abc123"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q: %s", want, instruction)
		}
	}

	// The same failure is reported exactly once: the second poll sees the
	// commit already recorded and stays quiet.
	again, err := watcher.Poll(context.Background())
	if err != nil || len(again) != 0 {
		t.Fatalf("second poll=%#v err=%v", again, err)
	}
	persisted, err := sessions.Load(context.Background(), "run")
	if err != nil || persisted.ChecksRef != "abc123" || persisted.ChecksConclusion != "failure" {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
}

func TestChecksWatcherStaysQuietForGreenAndUnfinishedChecks(t *testing.T) {
	green, _ := checksFixture(t, []ports.CheckRun{{Name: "build", Status: "completed", Conclusion: "success"}})
	completions, err := green.Poll(context.Background())
	if err != nil || len(completions) != 0 {
		t.Fatalf("a green pull request must not resume a turn: %#v err=%v", completions, err)
	}

	// A suite that is still running is not a result: reacting to a partial
	// failure would chase something the remaining runs may resolve.
	running, sessions := checksFixture(t, []ports.CheckRun{
		{Name: "build", Status: "completed", Conclusion: "failure"},
		{Name: "test", Status: "in_progress"},
	})
	completions, err = running.Poll(context.Background())
	if err != nil || len(completions) != 0 {
		t.Fatalf("completions=%#v err=%v", completions, err)
	}
	persisted, err := sessions.Load(context.Background(), "run")
	if err != nil || persisted.ChecksRef != "" {
		t.Fatalf("an unfinished suite must not be recorded as handled: %#v err=%v", persisted, err)
	}
}

func TestChecksWatcherIgnoresSessionsWhoseWorkspaceIsGone(t *testing.T) {
	store := newMemoryStore()
	store.state.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy"}}
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run"] = ports.ImplementationSession{
		ID: "run", Repository: "eggy", Branch: "eggy/run", Commit: "abc123",
		PullRequestNumber: 7, Phase: ports.PhaseCompleted,
	}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	watcher := NewChecksWatcher(store, sessions, newFakeThreadStore(), &fakeRepositoryReader{
		checks: []ports.CheckRun{{Name: "test", Status: "completed", Conclusion: "failure"}},
	})

	completions, err := watcher.Poll(context.Background())
	if err != nil || len(completions) != 0 {
		t.Fatalf("a reaped checkout leaves nothing to resume in place: %#v err=%v", completions, err)
	}
}
