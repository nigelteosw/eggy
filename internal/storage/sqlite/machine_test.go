package sqlite

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// A fresh home has no state row at all, and must read as the zero state
// rather than as an error: first boot is the common case.
func TestStateStoreStartsEmptyAndRoundTripsEveryField(t *testing.T) {
	state := newTestStore(t, 0).State()
	ctx := as("owner")
	initial, err := state.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Version != 0 || len(initial.Approvals) != 0 || len(initial.ProcessedEvents) != 0 {
		t.Fatalf("initial=%#v", initial)
	}
	seen := time.Date(2026, 9, 6, 10, 0, 0, 0, time.FixedZone("SGT", 8*3600))
	updated, err := state.Update(ctx, 0, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeStrict
		next.Approvals = map[string]approvals.Approval{"a1": {ID: "a1", Action: "tool_call", Status: approvals.Pending, CreatedAt: seen, ExpiresAt: seen.Add(30 * time.Minute)}}
		next.ProcessedEvents = map[string]time.Time{"telegram:1": seen}
		next.ProactiveMessages = []time.Time{seen, seen.Add(time.Hour)}
		next.Agent = ports.AgentRuntimeState{SelectedModel: "deepseek-pro", ReasoningEffort: "high", HideThinking: true}
		next.Repositories = map[string]ports.Repository{"eggy": {Name: "eggy", CloneURL: "https://example/eggy.git"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 1 || updated.SchemaVersion != MachineStateVersion {
		t.Fatalf("updated=%#v", updated)
	}
	reloaded, err := state.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ApprovalMode != ports.ModeStrict || reloaded.Agent.SelectedModel != "deepseek-pro" || !reloaded.Agent.HideThinking {
		t.Fatalf("reloaded=%#v", reloaded)
	}
	if got := reloaded.Approvals["a1"]; got.Action != "tool_call" || !got.CreatedAt.Equal(seen) {
		t.Fatalf("approval=%#v", got)
	}
	if got := reloaded.ProcessedEvents["telegram:1"]; !got.Equal(seen) {
		t.Fatalf("processed=%v", got)
	}
	if len(reloaded.ProactiveMessages) != 2 || !reloaded.ProactiveMessages[0].Equal(seen) {
		t.Fatalf("proactive=%v", reloaded.ProactiveMessages)
	}
	if reloaded.Repositories["eggy"].CloneURL != "https://example/eggy.git" {
		t.Fatalf("repositories=%#v", reloaded.Repositories)
	}
}

// The version check is what keeps two writers from silently overwriting each
// other. A stale expectation must fail rather than win.
func TestStateStoreRefusesAStaleUpdateAndWritesNothing(t *testing.T) {
	state := newTestStore(t, 0).State()
	ctx := as("owner")
	if _, err := state.Update(ctx, 0, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeNormal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := state.Update(ctx, 0, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeAuto
		return nil
	})
	if !errors.Is(err, ports.ErrStateVersionConflict) {
		t.Fatalf("err=%v", err)
	}
	current, err := state.Load(ctx)
	if err != nil || current.ApprovalMode != ports.ModeNormal || current.Version != 1 {
		t.Fatalf("current=%#v err=%v", current, err)
	}
}

// A mutation that fails partway leaves the previous state: the read and the
// write are one transaction, so there is no half-applied update to recover.
func TestStateStoreRollsBackAFailedMutation(t *testing.T) {
	state := newTestStore(t, 0).State()
	ctx := as("owner")
	if _, err := state.Update(ctx, 0, func(next *ports.State) error {
		next.Approvals["keep"] = approvals.Approval{ID: "keep"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("mutation failed")
	if _, err := state.Update(ctx, 1, func(next *ports.State) error {
		next.Approvals["gone"] = approvals.Approval{ID: "gone"}
		delete(next.Approvals, "keep")
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("err=%v", err)
	}
	current, err := state.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := current.Approvals["keep"]; !ok || len(current.Approvals) != 1 || current.Version != 1 {
		t.Fatalf("current=%#v", current)
	}
}

// The retired auto-mode boolean has to survive a round trip until the
// approval service clears it, or an owner's existing bypass would silently
// come back on at the first write.
func TestStateStoreCarriesTheRetiredAutoModeBoolean(t *testing.T) {
	state := newTestStore(t, 0).State()
	ctx := as("owner")
	if _, err := state.Update(ctx, 0, func(next *ports.State) error {
		next.ApprovalAutoMode = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load(ctx)
	if err != nil || !loaded.ApprovalAutoMode {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err := state.Update(ctx, 1, func(next *ports.State) error {
		next.ApprovalMode = ports.ModeAuto
		next.ApprovalAutoMode = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := state.Load(ctx)
	if err != nil || cleared.ApprovalAutoMode || cleared.ApprovalMode != ports.ModeAuto {
		t.Fatalf("cleared=%#v err=%v", cleared, err)
	}
}

func TestScheduleStoreCreateIsUniqueAndTimesKeepTheirOffset(t *testing.T) {
	schedules := newTestStore(t, 0).Schedules()
	singapore := time.FixedZone("SGT", 8*3600)
	next := time.Date(2026, 9, 7, 9, 0, 0, 0, singapore)
	job := ports.Schedule{ID: "morning", Kind: ports.ScheduleRecurring, Execution: ports.ScheduleExecutionAgent,
		Instruction: "status", Expression: "0 9 * * *", NextRun: next, Enabled: true}
	if err := schedules.Create(as("owner"), job); err != nil {
		t.Fatal(err)
	}
	if err := schedules.Create(as("owner"), job); err == nil {
		t.Fatal("a second schedule took an id that was already taken")
	}
	stored, err := schedules.Get(as("owner"), "morning")
	if err != nil {
		t.Fatal(err)
	}
	// A "09:00 daily" job computed in the owner's location must come back
	// with that location's offset, not normalized to UTC.
	if _, offset := stored.NextRun.Zone(); offset != 8*3600 {
		t.Fatalf("offset=%d next=%v", offset, stored.NextRun)
	}
	if !stored.NextRun.Equal(next) || stored.Execution != ports.ScheduleExecutionAgent || !stored.Enabled {
		t.Fatalf("stored=%#v", stored)
	}
}

func TestScheduleStoreReportsMissingJobsAndDeletesIdempotently(t *testing.T) {
	schedules := newTestStore(t, 0).Schedules()
	if _, err := schedules.Get(as("owner"), "absent"); !errors.Is(err, ports.ErrScheduleNotFound) {
		t.Fatalf("err=%v", err)
	}
	if err := schedules.Update(as("owner"), "absent", func(*ports.Schedule) error { return nil }); !errors.Is(err, ports.ErrScheduleNotFound) {
		t.Fatalf("err=%v", err)
	}
	// Cancelling a job that is already gone is what the owner asked for.
	if err := schedules.Delete(as("owner"), "absent"); err != nil {
		t.Fatal(err)
	}
	if err := schedules.Create(as("owner"), ports.Schedule{ID: "../escape", Kind: ports.ScheduleExact, Instruction: "no"}); err == nil {
		t.Fatal("an unusable schedule id was accepted")
	}
}

func TestAuthRecordsRoundTripAndDelete(t *testing.T) {
	records := newTestStore(t, 0).Auth()
	if _, err := records.Read("google", "workspace"); err == nil {
		t.Fatal("an absent record loaded")
	}
	if err := records.Write("google", "workspace", []byte(`{"ciphertext":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	stored, err := records.Read("google", "workspace")
	if err != nil || string(stored) != `{"ciphertext":"abc"}` {
		t.Fatalf("stored=%s err=%v", stored, err)
	}
	if err := records.Update("google", "workspace", func(current json.RawMessage) (json.RawMessage, error) {
		if string(current) != `{"ciphertext":"abc"}` {
			t.Fatalf("update saw %s", current)
		}
		return json.RawMessage(`{"ciphertext":"def"}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := records.Delete("google", "workspace"); err != nil {
		t.Fatal(err)
	}
	if _, err := records.Read("google", "workspace"); err == nil {
		t.Fatal("a deleted record still loads")
	}
	if err := records.Write("bad name", "workspace", []byte(`{}`)); err == nil {
		t.Fatal("an unusable section name was accepted")
	}
}

// A database written by a newer build must be refused rather than read with
// the wrong shape: the alternative is a silent downgrade that drops fields
// the newer binary was keeping.
func TestOpenRefusesANewerMachineStateVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eggy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE schema_meta SET value = ? WHERE key = ?`, MachineStateVersion+1, machineStateVersionKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a home from a newer Eggy opened anyway")
	}
}
