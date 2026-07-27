package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	sessionjson "github.com/nigelteosw/eggy/plugins/sessions/jsonfile"
)

// TestAppRecoverInterruptedFlipsRunningSessionsAfterRestart is the
// integration counterpart of the unit-level coding-service recovery test: it
// proves App's actual dependency wiring -- NewApp constructing the coding
// service against config.DataDir/sessions -- really is the same store a
// session was left running in before an unclean restart.
func TestAppRecoverInterruptedFlipsRunningSessionsAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	changeStore := sessionjson.OpenChanges(filepath.Join(dataDir, "changes"))
	if _, err := changeStore.Create(context.Background(), ports.Change{ID: "run-1", Repository: "eggy", Phase: ports.PhaseRunning}); err != nil {
		t.Fatal(err)
	}
	cfg := appTestConfig(dataDir)
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	count, err := app.changes.MarkInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	change, err := changeStore.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if change.Phase != ports.PhaseBlocked {
		t.Fatalf("change=%#v, want PhaseBlocked", change)
	}
}

func TestImportLegacyCodingRunsIsANoOpWithoutAStateFile(t *testing.T) {
	dir := t.TempDir()
	changeStore := sessionjson.OpenChanges(filepath.Join(dir, "sessions"))
	imported, err := importLegacyCodingRuns(context.Background(), filepath.Join(dir, "state.json"), changeStore, time.Now)
	if err != nil || imported != 0 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}
}

// TestImportLegacyCodingRunsImportsOrphanedRunsFromARepresentativeStateFile
// loads a schema-2 state file shaped like a real deployed instance (the same
// fixture shape used by the state-store migration tests), containing two
// coding_runs: one with no matching session on disk (the orphan a dual-write
// gap could have left behind) and one whose session already exists and is
// further along than the legacy run record. Only the orphan should be
// imported, and the existing session must be left untouched since it is the
// canonical source once a session exists at all.
func TestImportLegacyCodingRunsImportsOrphanedRunsFromARepresentativeStateFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	workspace := filepath.Join(dir, "runs", "run-orphan")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema_version": 2,
  "version": 9,
  "approvals": {},
  "schedules": {},
  "coding_runs": {
    "run-orphan": {"id":"run-orphan","repository":"eggy","workspace":"` + workspace + `","branch":"eggy/run-orphan","base_revision":"abc123","status":"completed","diff":"diff","validation":"tests pass","started_at":"2026-07-19T00:00:00Z","finished_at":"2026-07-19T00:05:00Z"},
    "run-canonical": {"id":"run-canonical","repository":"eggy","workspace":"/data/runs/run-canonical","branch":"eggy/run-canonical","status":"running","started_at":"2026-07-19T00:00:00Z"}
  },
  "repositories": {"eggy":{"Name":"eggy","CloneURL":"https://github.com/nigelteosw/eggy.git","BaseBranch":"main"}}
}`
	if err := os.WriteFile(statePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionRoot := filepath.Join(dir, "sessions")
	changeStore := sessionjson.OpenChanges(sessionRoot)
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if _, err := changeStore.Create(context.Background(), ports.Change{ID: "run-canonical", Repository: "eggy", Branch: "eggy/run-canonical", Phase: ports.PhaseCompleted, Diff: "already progressed further"}); err != nil {
		t.Fatal(err)
	}

	imported, err := importLegacyCodingRuns(context.Background(), statePath, changeStore, func() time.Time { return now })
	if err != nil || imported != 1 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}

	orphan, err := changeStore.Load(context.Background(), "run-orphan")
	if err != nil {
		t.Fatal(err)
	}
	if orphan.Repository != "eggy" || orphan.Branch != "eggy/run-orphan" || orphan.BaseRevision != "abc123" || orphan.Diff != "diff" || orphan.Validation != "tests pass" || orphan.Phase != ports.PhaseCompleted {
		t.Fatalf("orphan=%#v", orphan)
	}

	canonical, err := changeStore.Load(context.Background(), "run-canonical")
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Diff != "already progressed further" || canonical.Phase != ports.PhaseCompleted {
		t.Fatalf("canonical session was overwritten by the legacy import: %#v", canonical)
	}

	// Rerunning must be idempotent: both sessions already exist now, so a
	// second pass must not error, duplicate, or overwrite anything.
	imported, err = importLegacyCodingRuns(context.Background(), statePath, changeStore, func() time.Time { return now })
	if err != nil || imported != 0 {
		t.Fatalf("rerun imported=%d err=%v", imported, err)
	}
}

// TestImportLegacyCodingRunsBlocksRunsWhoseWorkspaceIsGone proves a legacy
// run whose workspace directory no longer exists on disk is imported as
// PhaseBlocked regardless of its recorded status, so nothing ever
// auto-resumes (replays) implementation work against a workspace that is
// gone.
func TestImportLegacyCodingRunsBlocksRunsWhoseWorkspaceIsGone(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	body := `{
  "schema_version": 2,
  "version": 3,
  "approvals": {},
  "schedules": {},
  "coding_runs": {
    "run-gone": {"id":"run-gone","repository":"eggy","workspace":"/data/runs/does-not-exist","branch":"eggy/run-gone","base_revision":"abc123","status":"running","started_at":"2026-07-19T00:00:00Z"}
  }
}`
	if err := os.WriteFile(statePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	changeStore := sessionjson.OpenChanges(filepath.Join(dir, "sessions"))
	imported, err := importLegacyCodingRuns(context.Background(), statePath, changeStore, time.Now)
	if err != nil || imported != 1 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}
	change, err := changeStore.Load(context.Background(), "run-gone")
	if err != nil {
		t.Fatal(err)
	}
	if change.Phase != ports.PhaseBlocked {
		t.Fatalf("change=%#v, want PhaseBlocked since its workspace no longer exists", change)
	}
}
