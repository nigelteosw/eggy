package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sessionjson "github.com/nigelteosw/eggy/internal/adapters/sessions/jsonfile"
	"github.com/nigelteosw/eggy/internal/ports"
)

// TestAppRecoverInterruptedFlipsRunningSessionsAfterRestart is the
// integration counterpart of the unit-level coding-service recovery test: it
// proves App's actual dependency wiring -- NewApp constructing the coding
// service against config.DataDir/sessions -- really is the same store a
// session was left running in before an unclean restart.
func TestAppRecoverInterruptedFlipsRunningSessionsAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	sessionStore := sessionjson.Open(filepath.Join(dataDir, "sessions"))
	if _, err := sessionStore.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Repository: "eggy", Workspace: filepath.Join(dataDir, "runs", "run-1"), Phase: ports.PhaseRunning}); err != nil {
		t.Fatal(err)
	}
	cfg := appTestConfig(dataDir)
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	count, err := app.coding.RecoverInterrupted(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	session, err := sessionStore.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != ports.PhaseInterrupted {
		t.Fatalf("session=%#v, want PhaseInterrupted", session)
	}
}

func TestImportLegacyCodingRunsIsANoOpWithoutAStateFile(t *testing.T) {
	dir := t.TempDir()
	sessionStore := sessionjson.Open(filepath.Join(dir, "sessions"))
	imported, err := importLegacyCodingRuns(context.Background(), filepath.Join(dir, "state.json"), sessionStore, time.Now)
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
	sessionStore := sessionjson.Open(sessionRoot)
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if _, err := sessionStore.Create(context.Background(), ports.ImplementationSession{ID: "run-canonical", Repository: "eggy", Workspace: "/data/runs/run-canonical", Branch: "eggy/run-canonical", Phase: ports.PhaseReady, Diff: "already progressed further"}); err != nil {
		t.Fatal(err)
	}

	imported, err := importLegacyCodingRuns(context.Background(), statePath, sessionStore, func() time.Time { return now })
	if err != nil || imported != 1 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}

	orphan, err := sessionStore.Load(context.Background(), "run-orphan")
	if err != nil {
		t.Fatal(err)
	}
	if orphan.Repository != "eggy" || orphan.Branch != "eggy/run-orphan" || orphan.BaseRevision != "abc123" || orphan.Diff != "diff" || orphan.Validation != "tests pass" || orphan.Phase != ports.PhaseReady {
		t.Fatalf("orphan=%#v", orphan)
	}

	canonical, err := sessionStore.Load(context.Background(), "run-canonical")
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Diff != "already progressed further" || canonical.Phase != ports.PhaseReady {
		t.Fatalf("canonical session was overwritten by the legacy import: %#v", canonical)
	}

	// Rerunning must be idempotent: both sessions already exist now, so a
	// second pass must not error, duplicate, or overwrite anything.
	imported, err = importLegacyCodingRuns(context.Background(), statePath, sessionStore, func() time.Time { return now })
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
	sessionStore := sessionjson.Open(filepath.Join(dir, "sessions"))
	imported, err := importLegacyCodingRuns(context.Background(), statePath, sessionStore, time.Now)
	if err != nil || imported != 1 {
		t.Fatalf("imported=%d err=%v", imported, err)
	}
	session, err := sessionStore.Load(context.Background(), "run-gone")
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != ports.PhaseBlocked {
		t.Fatalf("session=%#v, want PhaseBlocked since its workspace no longer exists", session)
	}
}
