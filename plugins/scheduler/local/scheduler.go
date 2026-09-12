package local

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Store is the durable half of scheduling: the records themselves, with no
// opinion about cron syntax or when a job is due. It is declared here, at the
// consumer, so the scheduler names only what it calls and the SQLite store
// satisfies it without knowing this package exists.
//
// Every method acts as the principal on the context: List, Create, Update
// and Delete see one account's jobs. ListAll is the exception the tick
// needs, and what it returns carries each job's Owner so the scheduler can
// act as that account for the claim and the completion.
type Store interface {
	List(context.Context) ([]ports.Schedule, error)
	ListAll(context.Context) ([]ports.Schedule, error)
	Create(context.Context, ports.Schedule) error
	Update(ctx context.Context, id string, mutate func(*ports.Schedule) error) error
	Delete(ctx context.Context, id string) error
}

// asOwner returns ctx acting as the job's owner, for the steps the tick takes
// on a job it found through ListAll.
func asOwner(ctx context.Context, schedule ports.Schedule) context.Context {
	return ports.WithPrincipal(ctx, ports.Principal{AccountID: schedule.Owner})
}

// Scheduler owns the timing rules -- cron parsing, what is due, what happens
// after a run succeeds or fails -- over jobs a Store keeps. It holds no
// schedule state of its own, so every claim and completion is read back from
// the store rather than cached across ticks.
type Scheduler struct{ store Store }

func New(store Store) *Scheduler { return &Scheduler{store: store} }

func (s *Scheduler) Add(ctx context.Context, schedule ports.Schedule) error {
	if schedule.ID == "" || schedule.Instruction == "" {
		return errors.New("schedule id and instruction are required")
	}
	if schedule.NextRun.IsZero() {
		return errors.New("schedule next_run is required")
	}
	if schedule.Kind == ports.ScheduleRecurring {
		if _, err := ParseCron(schedule.Expression); err != nil {
			return err
		}
	}
	switch schedule.Execution {
	case "":
		schedule.Execution = ports.ScheduleExecutionAgent
	case ports.ScheduleExecutionAgent, ports.ScheduleExecutionMessage:
	default:
		return fmt.Errorf("unknown schedule execution %q", schedule.Execution)
	}
	return s.store.Create(ctx, schedule)
}

func (s *Scheduler) Remove(ctx context.Context, id string) error { return s.store.Delete(ctx, id) }

// List returns the acting account's schedules, for the /schedules command
// and the web UI.
func (s *Scheduler) List(ctx context.Context) ([]ports.Schedule, error) { return s.store.List(ctx) }

// Due claims every schedule whose next run has arrived by stamping it with a
// pending run, so a second tick -- or a second reader -- never picks up a job
// already in flight.
func (s *Scheduler) Due(ctx context.Context, now time.Time) ([]ports.Schedule, error) {
	schedules, err := s.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	due := make([]ports.Schedule, 0)
	for _, schedule := range schedules {
		if !schedule.Enabled || !schedule.PendingRun.IsZero() || schedule.NextRun.After(now) {
			continue
		}
		claimed := schedule
		err := s.store.Update(asOwner(ctx, schedule), schedule.ID, func(current *ports.Schedule) error {
			// Re-check under the file lock: the listing above is a snapshot.
			if !current.Enabled || !current.PendingRun.IsZero() || current.NextRun.After(now) {
				return errNotDue
			}
			current.PendingRun = current.NextRun
			claimed = *current
			return nil
		})
		if errors.Is(err, errNotDue) || errors.Is(err, ports.ErrScheduleNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		due = append(due, claimed)
	}
	return due, nil
}

var errNotDue = errors.New("schedule is no longer due")

func (s *Scheduler) Complete(ctx context.Context, id string, scheduledFor, completedAt time.Time) error {
	return s.store.Update(ctx, id, func(schedule *ports.Schedule) error {
		if !schedule.PendingRun.Equal(scheduledFor) {
			return errors.New("schedule completion does not match pending run")
		}
		schedule.LastRun, schedule.PendingRun = scheduledFor, time.Time{}
		switch schedule.Kind {
		case ports.ScheduleExact:
			schedule.Enabled = false
		case ports.ScheduleRecurring:
			cron, err := ParseCron(schedule.Expression)
			if err != nil {
				return err
			}
			next, err := cron.Next(completedAt.In(schedule.NextRun.Location()))
			if err != nil {
				return err
			}
			schedule.NextRun = next
		default:
			schedule.Enabled = false
		}
		return nil
	})
}

// Disable switches a job off and releases any claim on it, for a job whose
// owner can no longer receive it. The record stays: it is theirs.
func (s *Scheduler) Disable(ctx context.Context, id string) error {
	return s.store.Update(ctx, id, func(schedule *ports.Schedule) error {
		schedule.Enabled = false
		schedule.PendingRun = time.Time{}
		return nil
	})
}

func (s *Scheduler) Fail(ctx context.Context, id string, scheduledFor time.Time) error {
	return s.store.Update(ctx, id, func(schedule *ports.Schedule) error {
		if !schedule.PendingRun.Equal(scheduledFor) {
			return errors.New("schedule failure does not match pending run")
		}
		schedule.PendingRun = time.Time{}
		return nil
	})
}

// Recover clears pending runs left behind by a process that died mid-run, so
// those schedules become due again instead of stalling forever.
func (s *Scheduler) Recover(ctx context.Context) error {
	schedules, err := s.store.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if schedule.PendingRun.IsZero() {
			continue
		}
		err := s.store.Update(asOwner(ctx, schedule), schedule.ID, func(current *ports.Schedule) error {
			current.PendingRun = time.Time{}
			return nil
		})
		if err != nil && !errors.Is(err, ports.ErrScheduleNotFound) {
			return err
		}
	}
	return nil
}

func (s *Scheduler) Next(expression string, after time.Time) (time.Time, error) {
	cron, err := ParseCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	return cron.Next(after)
}
