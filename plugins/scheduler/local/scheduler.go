package local

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/scheduler/cronfile"
)

// Scheduler owns the timing rules -- cron parsing, what is due, what happens
// after a run succeeds or fails -- over jobs kept as files in <home>/cron by
// cronfile.Store. It holds no schedule state of its own, so a job an owner
// edits on disk takes effect on the next tick.
type Scheduler struct{ store *cronfile.Store }

func New(store *cronfile.Store) *Scheduler { return &Scheduler{store: store} }

func (s *Scheduler) Add(_ context.Context, schedule ports.Schedule) error {
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
	return s.store.Create(schedule)
}

func (s *Scheduler) Remove(_ context.Context, id string) error { return s.store.Delete(id) }

// List returns every schedule, for the /schedules command and the web UI.
func (s *Scheduler) List(context.Context) ([]ports.Schedule, error) { return s.store.List() }

// Due claims every schedule whose next run has arrived by stamping it with a
// pending run, so a second tick -- or a second reader -- never picks up a job
// already in flight.
func (s *Scheduler) Due(_ context.Context, now time.Time) ([]ports.Schedule, error) {
	schedules, err := s.store.List()
	if err != nil {
		return nil, err
	}
	due := make([]ports.Schedule, 0)
	for _, schedule := range schedules {
		if !schedule.Enabled || !schedule.PendingRun.IsZero() || schedule.NextRun.After(now) {
			continue
		}
		claimed := schedule
		err := s.store.Update(schedule.ID, func(current *ports.Schedule) error {
			// Re-check under the file lock: the listing above is a snapshot.
			if !current.Enabled || !current.PendingRun.IsZero() || current.NextRun.After(now) {
				return errNotDue
			}
			current.PendingRun = current.NextRun
			claimed = *current
			return nil
		})
		if errors.Is(err, errNotDue) || errors.Is(err, cronfile.ErrNotFound) {
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

func (s *Scheduler) Complete(_ context.Context, id string, scheduledFor, completedAt time.Time) error {
	return s.store.Update(id, func(schedule *ports.Schedule) error {
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

func (s *Scheduler) Fail(_ context.Context, id string, scheduledFor time.Time) error {
	return s.store.Update(id, func(schedule *ports.Schedule) error {
		if !schedule.PendingRun.Equal(scheduledFor) {
			return errors.New("schedule failure does not match pending run")
		}
		schedule.PendingRun = time.Time{}
		return nil
	})
}

// Recover clears pending runs left behind by a process that died mid-run, so
// those schedules become due again instead of stalling forever.
func (s *Scheduler) Recover(_ context.Context) error {
	schedules, err := s.store.List()
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if schedule.PendingRun.IsZero() {
			continue
		}
		err := s.store.Update(schedule.ID, func(current *ports.Schedule) error {
			current.PendingRun = time.Time{}
			return nil
		})
		if err != nil && !errors.Is(err, cronfile.ErrNotFound) {
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
