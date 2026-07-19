package local

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestSchedulerDeliversExactOnceAndAdvancesRecurring(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newStateStore()
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
	state, _ := store.Load(context.Background())
	if !state.Schedules["once"].Enabled || state.Schedules["once"].PendingRun.IsZero() || state.Schedules["cron"].PendingRun.IsZero() {
		t.Fatalf("due work was not retained pending completion: %#v", state.Schedules)
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
	state, _ = store.Load(context.Background())
	if state.Schedules["once"].Enabled || !state.Schedules["cron"].NextRun.Equal(time.Date(2026, 7, 19, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("completed schedules=%#v", state.Schedules)
	}
}

func TestSchedulerRestartCatchupEmitsRecurringOnlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 17, 0, 0, time.UTC)
	store := newStateStore()
	store.state.Schedules["cron"] = ports.Schedule{ID: "cron", Kind: ports.ScheduleRecurring, Expression: "*/5 * * * *", NextRun: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), Enabled: true}
	due, err := New(store).Due(context.Background(), now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	if err := New(store).Complete(context.Background(), "cron", time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), now); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(context.Background())
	if !state.Schedules["cron"].NextRun.Equal(time.Date(2026, 7, 19, 10, 20, 0, 0, time.UTC)) {
		t.Fatalf("next=%v", state.Schedules["cron"].NextRun)
	}
}

func TestSchedulerRecoveryRetriesUnfinishedDispatch(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	store := newStateStore()
	store.state.Schedules["once"] = ports.Schedule{ID: "once", Kind: ports.ScheduleExact, Instruction: "retry", NextRun: now, PendingRun: now, Enabled: true}
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

type stateStore struct {
	mu    sync.Mutex
	state ports.State
}

func newStateStore() *stateStore {
	return &stateStore{state: ports.State{SchemaVersion: 1, Schedules: map[string]ports.Schedule{}}}
}
func (s *stateStore) Load(context.Context) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}
func (s *stateStore) Update(_ context.Context, expected uint64, fn func(*ports.State) error) (ports.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Version != expected {
		return ports.State{}, errors.New("conflict")
	}
	if err := fn(&s.state); err != nil {
		return ports.State{}, err
	}
	s.state.Version++
	return s.state, nil
}
