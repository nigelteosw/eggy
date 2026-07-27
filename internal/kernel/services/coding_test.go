package services

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Session bookkeeping: the checkout belongs to the thread, so releasing a
// finished session must not destroy the directory the owner is still
// discussing.
func TestCleanupReleasesTheSessionButKeepsTheThreadsCheckout(t *testing.T) {
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run"] = ports.ImplementationSession{ID: "run", Workspace: "/tmp/runs/run", Phase: ports.PhaseReady}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)
	runner := &fakeWorkspaceRunner{workspace: "/tmp/runs/run"}

	if err := sessions.ReleaseWorkspace(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if runner.destroyed {
		t.Fatal("the thread's checkout must outlive the session that branched it")
	}
	persisted, err := sessions.Load(context.Background(), "run")
	if err != nil || persisted.Workspace != "" {
		t.Fatalf("workspace retained in session: %#v err=%v", persisted, err)
	}
}

func TestRecoverInterruptedMarksSessionsLeftRunningByARestart(t *testing.T) {
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["run"] = ports.ImplementationSession{ID: "run", Workspace: "/tmp/runs/run", Phase: ports.PhaseRunning}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)

	count, err := sessions.MarkInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	persisted, err := sessions.Load(context.Background(), "run")
	if err != nil || persisted.Phase != ports.PhaseInterrupted {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
}

func TestCleanupExpiredReleasesOnlySessionsFinishedBeforeTheCutoff(t *testing.T) {
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	sessionStore := newMemorySessionStore()
	sessionStore.sessions["old"] = ports.ImplementationSession{ID: "old", Workspace: "/tmp/runs/old", Phase: ports.PhaseCompleted, FinishedAt: base}
	sessionStore.sessions["recent"] = ports.ImplementationSession{ID: "recent", Workspace: "/tmp/runs/recent", Phase: ports.PhaseCompleted, FinishedAt: base.Add(2 * time.Hour)}
	sessions := NewImplementationSessions(sessionStore, SessionPolicy{}, time.Now)

	if err := sessions.ReleaseExpiredWorkspaces(context.Background(), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	old, err := sessions.Load(context.Background(), "old")
	if err != nil || old.Workspace != "" {
		t.Fatalf("old=%#v err=%v", old, err)
	}
	recent, err := sessions.Load(context.Background(), "recent")
	if err != nil || recent.Workspace == "" {
		t.Fatalf("recent=%#v err=%v", recent, err)
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
