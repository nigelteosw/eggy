package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// oldHome writes the three artifacts a pre-consolidation home carried and
// returns the LegacyHome pointing at them.
func oldHome(t *testing.T) (string, LegacyHome) {
	t.Helper()
	root := t.TempDir()
	legacy := LegacyHome{
		State: filepath.Join(root, "state.json"),
		Cron:  filepath.Join(root, "cron"),
		Auth:  filepath.Join(root, "auth.json"),
	}
	write(t, legacy.State, `{
  "schema_version": 5,
  "version": 12,
  "approval_mode": "strict",
  "approvals": {"a1": {"id": "a1", "action": "tool_call", "status": "pending", "created_at": "2026-09-06T10:00:00+08:00"}},
  "processed_events": {"telegram:99": "2026-09-06T10:00:00+08:00"},
  "proactive_messages": ["2026-09-06T09:00:00+08:00"],
  "agent": {"selected_model": "deepseek-pro", "reasoning_effort": "high"}
}`)
	if err := os.MkdirAll(legacy.Cron, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(legacy.Cron, "morning.yaml"), "id: morning\nkind: recurring\nexecution: agent\ninstruction: status\ncron: '0 9 * * *'\nnext_run: 2026-09-07T09:00:00+08:00\nenabled: true\n")
	write(t, filepath.Join(legacy.Cron, "once.yaml"), "id: once\nkind: exact\ninstruction: check oven\nnext_run: 2026-09-06T18:00:00+08:00\npending_run: 2026-09-06T18:00:00+08:00\nenabled: true\n")
	write(t, legacy.Auth, `{"version":1,"sections":{"google":{"workspace":{"version":1,"ciphertext":"google-sealed"}},"mcp":{"railway":{"version":1,"ciphertext":"mcp-sealed"}}}}`)
	return root, legacy
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportMovesAnOldHomeAndArchivesEverySource(t *testing.T) {
	store := newTestStore(t, 0)
	_, legacy := oldHome(t)
	report, err := store.ImportLegacy(as("owner"), legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Any() || report.Moved[importSchedules] != 2 || report.Moved[importAuth] != 2 {
		t.Fatalf("report=%#v", report)
	}
	// Imported records are unowned until the boot names their account.
	if err := store.MigrateAccounts(as("owner"), "owner", false); err != nil {
		t.Fatal(err)
	}

	state, err := store.State().Load(as("owner"))
	if err != nil {
		t.Fatal(err)
	}
	// The version carries over, or the first update after an upgrade would
	// collide with a reader that had already loaded the old file.
	if state.Version != 12 || state.ApprovalMode != ports.ModeStrict || state.Agent.SelectedModel != "deepseek-pro" {
		t.Fatalf("state=%#v", state)
	}
	if len(state.Approvals) != 1 || len(state.ProcessedEvents) != 1 || len(state.ProactiveMessages) != 1 {
		t.Fatalf("collections=%#v", state)
	}

	schedules, err := store.Schedules().List(as("owner"))
	if err != nil || len(schedules) != 2 {
		t.Fatalf("schedules=%#v err=%v", schedules, err)
	}
	if _, offset := schedules[0].NextRun.Zone(); offset != 8*3600 {
		t.Fatalf("offset=%d next=%v", offset, schedules[0].NextRun)
	}
	// A job that was in flight when the daemon stopped keeps its pending
	// stamp, so recovery still sees it and retries it exactly once.
	if !schedules[1].PendingRun.Equal(schedules[1].NextRun) {
		t.Fatalf("pending=%v next=%v", schedules[1].PendingRun, schedules[1].NextRun)
	}

	for _, record := range []struct{ section, key, want string }{
		{"google", "workspace", "google-sealed"},
		{"mcp", "railway", "mcp-sealed"},
	} {
		stored, err := store.Auth().Read(record.section, record.key)
		if err != nil {
			t.Fatalf("%s/%s: %v", record.section, record.key, err)
		}
		// The ciphertext arrives byte-for-byte: the migration never opened
		// the envelope, so no key was needed and none could be wrong.
		if !strings.Contains(string(stored), record.want) {
			t.Fatalf("%s/%s = %s", record.section, record.key, stored)
		}
	}

	for _, source := range []string{legacy.State, legacy.Cron, legacy.Auth} {
		if _, err := os.Lstat(source); !os.IsNotExist(err) {
			t.Fatalf("%s survived the import: %v", source, err)
		}
		if _, err := os.Lstat(source + archiveSuffix); err != nil {
			t.Fatalf("%s has no archive: %v", source, err)
		}
	}
}

// The second boot after an upgrade must do nothing at all -- not re-read the
// archives, not re-insert a schedule, not reset the state version.
func TestImportIsSkippedOnEveryLaterBoot(t *testing.T) {
	store := newTestStore(t, 0)
	_, legacy := oldHome(t)
	if _, err := store.ImportLegacy(as("owner"), legacy, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateAccounts(as("owner"), "owner", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.State().Update(as("owner"), 12, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeNormal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err := store.ImportLegacy(as("owner"), legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Any() {
		t.Fatalf("a second boot imported again: %#v", report)
	}
	state, err := store.State().Load(as("owner"))
	if err != nil || state.ApprovalMode != ports.ModeNormal || state.Version != 13 {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	schedules, err := store.Schedules().List(as("owner"))
	if err != nil || len(schedules) != 2 {
		t.Fatalf("schedules=%#v err=%v", schedules, err)
	}
}

// The gap between the committed import and the archived source is the one
// window a crash can land in. The next boot must close it by archiving, never
// by importing the same records twice.
func TestImportArchivesASourceLeftBehindByAnInterruptedBoot(t *testing.T) {
	store := newTestStore(t, 0)
	_, legacy := oldHome(t)
	if _, err := store.ImportLegacy(as("owner"), legacy, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateAccounts(as("owner"), "owner", false); err != nil {
		t.Fatal(err)
	}
	// Exactly what a crash between COMMIT and rename leaves: the marker is
	// recorded and the source is still sitting there.
	write(t, legacy.State, `{"schema_version": 5, "version": 999, "approval_mode": "auto"}`)
	report, err := store.ImportLegacy(as("owner"), legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Any() {
		t.Fatalf("the leftover source was imported a second time: %#v", report)
	}
	state, err := store.State().Load(as("owner"))
	if err != nil || state.Version != 12 || state.ApprovalMode != ports.ModeStrict {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	if _, err := os.Lstat(legacy.State); !os.IsNotExist(err) {
		t.Fatalf("the leftover source was not archived: %v", err)
	}
}

// A source that cannot be read must abort its own import whole: nothing
// written, no marker, and the file still in place for the retry after the
// owner repairs it.
func TestImportRollsBackAndRetriesAfterAFailure(t *testing.T) {
	store := newTestStore(t, 0)
	_, legacy := oldHome(t)
	broken := filepath.Join(legacy.Cron, "broken.yaml")
	write(t, broken, "id: broken\n\tinstruction: [\n")
	if _, err := store.ImportLegacy(as("owner"), legacy, nil); err == nil {
		t.Fatal("a corrupt schedule file was imported anyway")
	}
	schedules, err := store.Schedules().List(as("owner"))
	if err != nil || len(schedules) != 0 {
		t.Fatalf("a failed import left rows behind: %#v err=%v", schedules, err)
	}
	if _, err := os.Lstat(legacy.Cron); err != nil {
		t.Fatalf("a failed import archived its source: %v", err)
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	report, err := store.ImportLegacy(as("owner"), legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Moved[importSchedules] != 2 {
		t.Fatalf("report=%#v", report)
	}
}

// A home that never had the old artifacts is not a migration and must not
// look like one.
func TestImportOnAFreshHomeIsSilent(t *testing.T) {
	store := newTestStore(t, 0)
	root := t.TempDir()
	report, err := store.ImportLegacy(as("owner"), LegacyHome{
		State: filepath.Join(root, "state.json"),
		Cron:  filepath.Join(root, "cron"),
		Auth:  filepath.Join(root, "auth.json"),
	}, func() time.Time { return time.Unix(0, 0) })
	if err != nil || report.Any() {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}
