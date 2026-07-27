package local

import (
	"context"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/scheduler/cronfile"
)

// newCronStore backs the scheduler with a real cron directory, since that is
// now the whole of its persistence: there is no in-memory schedule state to
// fake.
func newCronStore(t *testing.T) *cronfile.Store {
	t.Helper()
	return cronfile.Open(t.TempDir())
}

func mustGet(t *testing.T, store *cronfile.Store, id string) ports.Schedule {
	t.Helper()
	schedule, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return schedule
}

func TestSchedulerDeliversExactOnceAndAdvancesRecurring(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newCronStore(t)
	scheduler := New(store)
	if err := scheduler.Add(context.Background(), ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "check oven", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(context.Background(), ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Instruction: "status", Expression: "*/5 * * * *", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	due, err := scheduler.Due(context.Background(), now.Add(time.Minute))
	if err != nil || len(due) != 2 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if once := mustGet(t, store, "once"); !once.Enabled || once.PendingRun.IsZero() {
		t.Fatalf("due work was not retained pending completion: %#v", once)
	}
	if recurring := mustGet(t, store, "cron"); recurring.PendingRun.IsZero() {
		t.Fatalf("due work was not retained pending completion: %#v", recurring)
	}
	due, _ = scheduler.Due(context.Background(), now.Add(time.Minute))
	if len(due) != 0 {
		t.Fatalf("duplicate due=%#v", due)
	}
	if err := scheduler.Complete(context.Background(), "once", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Complete(context.Background(), "cron", now, now.Add(time.Minute)); err != nil {
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
	if err := store.Put(ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Instruction: "status", Expression: "*/5 * * * *", NextRun: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	due, err := New(store).Due(context.Background(), now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if err := New(store).Complete(context.Background(), "cron", time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), now); err != nil {
		t.Fatal(err)
	}
	if next := mustGet(t, store, "cron").NextRun; !next.Equal(time.Date(2026, 7, 19, 10, 20, 0, 0, time.UTC)) {
		t.Fatalf("next=%v", next)
	}
}

func TestSchedulerRecoveryRetriesUnfinishedDispatch(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newCronStore(t)
	if err := store.Put(ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "retry", NextRun: now, PendingRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	scheduler := New(store)
	if err := scheduler.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	due, err := scheduler.Due(context.Background(), now.Add(time.Minute))
	if err != nil || len(due) != 1 || due[0].ID != "once" {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if err := scheduler.Fail(context.Background(), "once", now); err != nil {
		t.Fatal(err)
	}
	due, _ = scheduler.Due(context.Background(), now.Add(2*time.Minute))
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
	scheduler := New(store)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	if err := scheduler.Add(context.Background(), ports.Schedule{ID: "default", Kind: ports.ScheduleExact, Instruction: "check oven", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(context.Background(), ports.Schedule{ID: "reminder", Kind: ports.ScheduleExact, Execution: ports.ScheduleExecutionMessage, Instruction: "Take the bins out", NextRun: now, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Add(context.Background(), ports.Schedule{ID: "bad", Kind: ports.ScheduleExact, Execution: "nonsense", Instruction: "x", NextRun: now, Enabled: true}); err == nil {
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
	scheduler := New(store)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	schedule := ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "first", NextRun: now, Enabled: true}
	if err := scheduler.Add(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	schedule.Instruction = "second"
	if err := scheduler.Add(context.Background(), schedule); err == nil {
		t.Fatal("expected a duplicate schedule id to be rejected")
	}
	if instruction := mustGet(t, store, "once").Instruction; instruction != "first" {
		t.Fatalf("original schedule was overwritten: %q", instruction)
	}
}
