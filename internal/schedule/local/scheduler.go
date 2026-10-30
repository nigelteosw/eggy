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
// Scheduler evaluates recurring expressions in one zone, the deployment's.
// Reading the zone back from a stored time would not do: the store keeps only
// an offset, so a job would drift an hour across a daylight-saving change.
type Scheduler struct {
	store    Store
	location *time.Location
}

func New(store Store, location *time.Location) *Scheduler {
	if location == nil {
		location = time.UTC
	}
	return &Scheduler{store: store, location: location}
}

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
// already in flight. A job that cannot be claimed does not stop the others: it
// is reported in the error, which may come back alongside the jobs that were
// claimed, and those must still be dispatched or they stall until restart.
func (s *Scheduler) Due(ctx context.Context, now time.Time) ([]ports.Schedule, error) {
	schedules, err := s.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var claimErr error
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
			claimErr = errors.Join(claimErr, fmt.Errorf("claim schedule %s: %w", schedule.ID, err))
			continue
		}
		due = append(due, claimed)
	}
	return due, claimErr
}

var errNotDue = errors.New("schedule is no longer due")

// Complete records a run. A recurring job advances to its next run; a one-time
// job is finished and deleted, because a disabled record left behind reads as
// "overdue" on every list until someone removes it by hand.
func (s *Scheduler) Complete(ctx context.Context, id string, scheduledFor, completedAt time.Time) error {
	finished := false
	err := s.store.Update(ctx, id, func(schedule *ports.Schedule) error {
		if !schedule.PendingRun.Equal(scheduledFor) {
			return errors.New("schedule completion does not match pending run")
		}
		schedule.LastRun, schedule.PendingRun = scheduledFor, time.Time{}
		switch schedule.Kind {
		case ports.ScheduleRecurring:
			cron, err := ParseCron(schedule.Expression)
			if err != nil {
				return err
			}
			next, err := cron.Next(completedAt.In(s.location))
			if err != nil {
				return err
			}
			schedule.NextRun = next
		default:
			// Disabled first, so a failed delete below still never runs it twice.
			schedule.Enabled, finished = false, true
		}
		return nil
	})
	if err != nil || !finished {
		return err
	}
	return s.removeFinished(ctx, id)
}

// finishedOnce reports a one-time job that has already run, which an earlier
// build disabled rather than deleted. A job disabled before it ever ran has no
// LastRun and is kept: Disable promises its owner the record.
func finishedOnce(schedule ports.Schedule) bool {
	return schedule.Kind != ports.ScheduleRecurring && !schedule.Enabled && !schedule.LastRun.IsZero()
}

func (s *Scheduler) removeFinished(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil && !errors.Is(err, ports.ErrScheduleNotFound) {
		return err
	}
	return nil
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
// those schedules become due again instead of stalling forever, and deletes
// one-time jobs that already ran but were kept by an earlier build.
func (s *Scheduler) Recover(ctx context.Context) error {
	schedules, err := s.store.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if finishedOnce(schedule) {
			if err := s.removeFinished(asOwner(ctx, schedule), schedule.ID); err != nil {
				return err
			}
			continue
		}
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
	return cron.Next(after.In(s.location))
}
