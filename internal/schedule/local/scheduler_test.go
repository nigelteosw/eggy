package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

// newCronStore backs the scheduler with a real database, since that is now
// the whole of its persistence: there is no in-memory schedule state to fake,
// and claiming a due job is a transaction rather than a struct field.
func newCronStore(t *testing.T) *sqlitestore.ScheduleStore {
	t.Helper()
	database, err := sqlitestore.Open(filepath.Join(t.TempDir(), "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database.Schedules()
}

func as(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

func mustGet(t *testing.T, store *sqlitestore.ScheduleStore, id string) ports.Schedule {
	t.Helper()
	schedules, err := store.List(as("owner"))
	if err != nil {
		t.Fatalf("list for %s: %v", id, err)
	}
	for _, schedule := range schedules {
		if schedule.ID == id {
			return schedule
		}
	}
	t.Fatalf("schedule %s not found", id)
	return ports.Schedule{}
}

func TestSchedulerDeliversExactOnceAndAdvancesRecurring(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newCronStore(t)
	scheduler := New(store, time.UTC)
	if err := scheduler.Add(as("owner"), ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "check oven", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(as("owner"), ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Instruction: "status", Expression: "*/5 * * * *", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	due, err := scheduler.Due(as("owner"), now.Add(time.Minute))
	if err != nil || len(due) != 2 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if once := mustGet(t, store, "once"); !once.Enabled || once.PendingRun.IsZero() {
		t.Fatalf("due work was not retained pending completion: %#v", once)
	}
	if recurring := mustGet(t, store, "cron"); recurring.PendingRun.IsZero() {
		t.Fatalf("due work was not retained pending completion: %#v", recurring)
	}
	due, _ = scheduler.Due(as("owner"), now.Add(time.Minute))
	if len(due) != 0 {
		t.Fatalf("duplicate due=%#v", due)
	}
	if err := scheduler.Complete(as("owner"), "once", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Complete(as("owner"), "cron", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if once := mustGet(t, store, "once"); once.Enabled {
		t.Fatalf("completed exact schedule stayed enabled: %#v", once)
	}
	if recurring := mustGet(t, store, "cron"); !recurring.NextRun.Equal(time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("recurring next=%v", recurring.NextRun)
	}
}

func TestSchedulerRestartCatchupEmitsRecurringOnlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 17, 0, 0, time.UTC)
	store := newCronStore(t)
	if err := store.Create(as("owner"), ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Instruction: "status", Expression: "*/5 * * * *", NextRun: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	due, err := New(store, time.UTC).Due(as("owner"), now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if err := New(store, time.UTC).Complete(as("owner"), "cron", time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), now); err != nil {
		t.Fatal(err)
	}
	if next := mustGet(t, store, "cron").NextRun; !next.Equal(time.Date(2026, 7, 19, 10, 20, 0, 0, time.UTC)) {
		t.Fatalf("next=%v", next)
	}
}

func TestSchedulerRecoveryRetriesUnfinishedDispatch(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newCronStore(t)
	if err := store.Create(as("owner"), ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "retry", NextRun: now, PendingRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	scheduler := New(store, time.UTC)
	if err := scheduler.Recover(as("owner")); err != nil {
		t.Fatal(err)
	}
	due, err := scheduler.Due(as("owner"), now.Add(time.Minute))
	if err != nil || len(due) != 1 || due[0].ID != "once" {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if err := scheduler.Fail(as("owner"), "once", now); err != nil {
		t.Fatal(err)
	}
	due, _ = scheduler.Due(as("owner"), now.Add(2*time.Minute))
	if len(due) != 1 {
		t.Fatalf("failed work was not retried: %#v", due)
	}
}

// TestSchedulerNormalizesExecutionKind proves a schedule created without an
// explicit Execution (including every schedule persisted before the field
// existed) defaults to ScheduleExecutionAgent, and an explicit
// ScheduleExecutionMessage (a deterministic, pre-rendered reminder) is kept
// as-is rather than being coerced into an agent turn.
func TestSchedulerNormalizesExecutionKind(t *testing.T) {
	store := newCronStore(t)
	scheduler := New(store, time.UTC)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	if err := scheduler.Add(as("owner"), ports.Schedule{ID: "default", Kind: ports.ScheduleExact, Instruction: "check oven", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(as("owner"), ports.Schedule{ID: "reminder", Kind: ports.ScheduleExact, Execution: ports.ScheduleExecutionMessage, Instruction: "Take the bins out", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(as("owner"), ports.Schedule{ID: "bad", Kind: ports.ScheduleExact, Execution: "nonsense", Instruction: "x", NextRun: now, Enabled: true}); err == nil {
		t.Fatal("expected an unknown execution kind to be rejected")
	}
	if execution := mustGet(t, store, "default").Execution; execution != ports.ScheduleExecutionAgent {
		t.Fatalf("default execution=%q", execution)
	}
	if execution := mustGet(t, store, "reminder").Execution; execution != ports.ScheduleExecutionMessage {
		t.Fatalf("reminder execution=%q", execution)
	}
}

// TestSchedulerAddRejectsDuplicateID proves two schedules can never collapse
// into one cron file.
func TestSchedulerAddRejectsDuplicateID(t *testing.T) {
	store := newCronStore(t)
	scheduler := New(store, time.UTC)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	schedule := ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "first", NextRun: now, Enabled: true}
	if err := scheduler.Add(as("owner"), schedule); err != nil {
		t.Fatal(err)
	}
	schedule.Instruction = "second"
	if err := scheduler.Add(as("owner"), schedule); err == nil {
		t.Fatal("expected a duplicate schedule id to be rejected")
	}
	if instruction := mustGet(t, store, "once").Instruction; instruction != "first" {
		t.Fatalf("original schedule was overwritten: %q", instruction)
	}
}

// A recurring job is wall-clock time in the owner's zone, not the process's.
// "09:00 daily" in New York is 13:00 UTC in July, whatever zone the host is in.
func TestSchedulerEvaluatesRecurringJobsInTheOwnersZone(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	next, err := New(newCronStore(t), newYork).Next("0 9 * * *", after)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Equal(time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("next=%v, want 09:00 New York", next)
	}
}

// Completing a run across the March DST change keeps the wall-clock hour: the
// next 09:00 is 09:00 local on the new offset, not the old one carried over.
func TestSchedulerCompleteKeepsTheWallClockHourAcrossDST(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	store := newCronStore(t)
	pending := time.Date(2026, 3, 7, 14, 0, 0, 0, time.UTC) // 09:00 EST
	if err := store.Create(as("owner"), ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Instruction: "status", Expression: "0 9 * * *", NextRun: pending, PendingRun: pending, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := New(store, newYork).Complete(as("owner"), "cron", pending, pending.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if next := mustGet(t, store, "cron").NextRun; !next.Equal(time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("next=%v, want 09:00 EDT on 8 March (13:00 UTC)", next)
	}
}

// failingClaims refuses to claim one schedule, the way a record that fails
// validation or a transient write error would.
type failingClaims struct {
	Store
	poison string
}

func (s failingClaims) Update(ctx context.Context, id string, mutate func(*ports.Schedule) error) error {
	if id == s.poison {
		return errors.New("write refused")
	}
	return s.Store.Update(ctx, id, mutate)
}

// One job that cannot be claimed must not strand the jobs after it, which
// would otherwise sit pending until the next restart.
func TestSchedulerClaimFailureDoesNotStrandOtherDueJobs(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newCronStore(t)
	for _, id := range []string{"aaa-poison", "bbb-good"} {
		if err := store.Create(as("owner"), ports.Schedule{ID: id, Kind: ports.ScheduleExact, Instruction: "x", NextRun: now.Add(-time.Minute), Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := New(failingClaims{Store: store, poison: "aaa-poison"}, time.UTC).Due(as("owner"), now)
	if err == nil {
		t.Fatal("expected the refused claim to be reported")
	}
	if len(due) != 1 || due[0].ID != "bbb-good" {
		t.Fatalf("due=%+v, want the good job still claimed", due)
	}
}
