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
