package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

var _ ports.AgentRunStore = (*Store)(nil)

func agentRun(id, repository string, created time.Time) ports.AgentRun {
	return ports.AgentRun{
		ID: id, Repository: repository, Brief: "b-" + id, BriefDigest: "d-" + id,
		BaseBranch: "main", Branch: "eggy/" + id, State: ports.AgentRunQueued,
		PATGeneration: 1, MandateExpires: created.Add(24 * time.Hour),
		CreatedAt: created, UpdatedAt: created,
	}
}

func TestAgentRunCreateGetScopedToAccount(t *testing.T) {
	db := newTestStore(t, 0)
	created := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	want := agentRun("r1", "eggy", created)
	if err := db.CreateAgentRun(as("a"), want); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAgentRun(as("a"), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Brief != want.Brief || got.Branch != want.Branch || got.State != ports.AgentRunQueued {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := db.GetAgentRun(as("b"), "r1"); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("other account read = %v, want ErrAgentRunNotFound", err)
	}
	if _, err := db.GetAgentRun(context.Background(), "r1"); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("no principal read = %v, want ErrNoPrincipal", err)
	}
}

func TestAgentRunUpdateIsConditional(t *testing.T) {
	db := newTestStore(t, 0)
	ctx := as("a")
	if err := db.CreateAgentRun(ctx, agentRun("r1", "eggy", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetAgentRun(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	first := current
	first.State = ports.AgentRunRunning
	updated, err := db.UpdateAgentRun(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != current.Revision+1 {
		t.Fatalf("revision = %d, want %d", updated.Revision, current.Revision+1)
	}
	// The second writer still holds the old revision and must lose.
	second := current
	second.State = ports.AgentRunFailed
	if _, err := db.UpdateAgentRun(ctx, second); !errors.Is(err, ports.ErrAgentRunConflict) {
		t.Fatalf("stale update = %v, want ErrAgentRunConflict", err)
	}
}

func TestRepositoryEnablementPerAccount(t *testing.T) {
	db := newTestStore(t, 0)
	if err := db.SetRepositoryEnabled(as("a"), "eggy", true); err != nil {
		t.Fatal(err)
	}
	mine, err := db.EnabledRepositories(as("a"))
	if err != nil || len(mine) != 1 || mine[0] != "eggy" {
		t.Fatalf("a enabled = %v err=%v", mine, err)
	}
	theirs, err := db.EnabledRepositories(as("b"))
	if err != nil || len(theirs) != 0 {
		t.Fatalf("b enabled = %v err=%v", theirs, err)
	}
	if err := db.SetRepositoryEnabled(as("a"), "eggy", false); err != nil {
		t.Fatal(err)
	}
	mine, _ = db.EnabledRepositories(as("a"))
	if len(mine) != 0 {
		t.Fatalf("disable left %v", mine)
	}
}

func TestNextQueuedAgentRunOldestFirst(t *testing.T) {
	db := newTestStore(t, 0)
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if err := db.CreateAgentRun(as("a"), agentRun("later", "eggy", base.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateAgentRun(as("b"), agentRun("first", "eggy", base)); err != nil {
		t.Fatal(err)
	}
	next, ok, err := db.NextQueuedAgentRun(context.Background())
	if err != nil || !ok || next.ID != "first" {
		t.Fatalf("next = %+v ok=%v err=%v", next, ok, err)
	}
}

func TestAgentRunSchemaIsAdditive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eggy.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CreateAgentRun(as("a"), agentRun("r1", "eggy", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Opening again re-applies every CREATE ... IF NOT EXISTS and must keep the row.
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.GetAgentRun(as("a"), "r1"); err != nil {
		t.Fatalf("row lost on reopen: %v", err)
	}
}

// The worker has no request principal, so a run read back must carry the
// owner stored on its row. That is the only way the worker can act as it.
func TestAgentRunReadCarriesStoredOwner(t *testing.T) {
	db := newTestStore(t, 0)
	if err := db.CreateAgentRun(as("owner-b"), agentRun("r9", "eggy", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	next, ok, err := db.NextQueuedAgentRun(context.Background())
	if err != nil || !ok || next.AccountID != "owner-b" {
		t.Fatalf("next = %+v ok=%v err=%v", next, ok, err)
	}
}

func TestAgentRunEventsAreOrderedAndScopedToAccount(t *testing.T) {
	db := newTestStore(t, 0)
	created := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if err := db.CreateAgentRun(as("a"), agentRun("r1", "eggy", created)); err != nil {
		t.Fatal(err)
	}
	for _, event := range []ports.AgentRunEvent{
		{At: created, Kind: "started", Text: "run started"},
		{At: created.Add(time.Second), Kind: "tool", Text: "run_check: go test failed"},
	} {
		if err := db.AppendAgentRunEvent(as("a"), "r1", event); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ListAgentRunEvents(as("a"), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 || got[1].Kind != "tool" || got[1].Text != "run_check: go test failed" {
		t.Fatalf("events = %+v, want two ordered steps", got)
	}
	if err := db.AppendAgentRunEvent(as("b"), "r1", ports.AgentRunEvent{Kind: "x"}); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("other account append = %v, want ErrAgentRunNotFound", err)
	}
	other, err := db.ListAgentRunEvents(as("b"), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other account sees %d events", len(other))
	}
}

// Steps written at the same moment must each get a number of their own: the
// record is ordered by them, so a collision would drop a step.
func TestAgentRunEventsConcurrentAppendsGetDistinctNumbers(t *testing.T) {
	db := newTestStore(t, 0)
	if err := db.CreateAgentRun(as("a"), agentRun("r1", "eggy", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	const writers = 20
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			errs <- db.AppendAgentRunEvent(as("a"), "r1", ports.AgentRunEvent{At: time.Now().UTC(), Kind: "tool", Text: "step"})
		}()
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ListAgentRunEvents(as("a"), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != writers {
		t.Fatalf("recorded %d steps, want %d", len(got), writers)
	}
	for i, step := range got {
		if step.Seq != i+1 {
			t.Fatalf("step %d has seq %d, want %d", i, step.Seq, i+1)
		}
	}
}

// Deleting a finished run removes it and its steps for its owner. A run still in
// flight cannot be deleted, and another account sees the run as not found.
func TestDeleteAgentRunRemovesFinishedRunAndSteps(t *testing.T) {
	db := newTestStore(t, 0)
	created := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	done := agentRun("done", "eggy", created)
	done.State = ports.AgentRunCompleted
	active := agentRun("active", "eggy", created)
	active.State = ports.AgentRunRunning
	for _, run := range []ports.AgentRun{done, active} {
		if err := db.CreateAgentRun(as("a"), run); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AppendAgentRunEvent(as("a"), "done", ports.AgentRunEvent{At: created, Kind: "started", Text: "go"}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteAgentRun(as("b"), "done"); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("other account delete = %v, want ErrAgentRunNotFound", err)
	}
	if err := db.DeleteAgentRun(as("a"), "active"); !errors.Is(err, ports.ErrAgentRunActive) {
		t.Fatalf("active delete = %v, want ErrAgentRunActive", err)
	}
	if err := db.DeleteAgentRun(as("a"), "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAgentRun(as("a"), "done"); !errors.Is(err, ports.ErrAgentRunNotFound) {
		t.Fatalf("deleted run still readable: %v", err)
	}
	steps, err := db.ListAgentRunEvents(as("a"), "done")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps survived the delete: %+v", steps)
	}
}
