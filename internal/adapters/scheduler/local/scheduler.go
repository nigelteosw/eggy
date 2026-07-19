package local

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type Scheduler struct{ store ports.StateStore }

func New(store ports.StateStore) *Scheduler { return &Scheduler{store: store} }

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
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		if state.Schedules == nil {
			state.Schedules = map[string]ports.Schedule{}
		}
		if _, exists := state.Schedules[schedule.ID]; exists {
			return fmt.Errorf("schedule %q already exists", schedule.ID)
		}
		state.Schedules[schedule.ID] = schedule
		return nil
	})
	return err
}

func (s *Scheduler) Remove(ctx context.Context, id string) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error { delete(state.Schedules, id); return nil })
	return err
}

func (s *Scheduler) Due(ctx context.Context, now time.Time) ([]ports.Schedule, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	due := make([]ports.Schedule, 0)
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		for id, schedule := range state.Schedules {
			if !schedule.Enabled || !schedule.PendingRun.IsZero() || schedule.NextRun.After(now) {
				continue
			}
			schedule.PendingRun = schedule.NextRun
			state.Schedules[id] = schedule
			due = append(due, schedule)
		}
		return nil
	})
	return due, err
}

func (s *Scheduler) Complete(ctx context.Context, id string, scheduledFor, completedAt time.Time) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		schedule, ok := state.Schedules[id]
		if !ok || !schedule.PendingRun.Equal(scheduledFor) {
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
		state.Schedules[id] = schedule
		return nil
	})
	return err
}

func (s *Scheduler) Fail(ctx context.Context, id string, scheduledFor time.Time) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		schedule, ok := state.Schedules[id]
		if !ok || !schedule.PendingRun.Equal(scheduledFor) {
			return errors.New("schedule failure does not match pending run")
		}
		schedule.PendingRun = time.Time{}
		state.Schedules[id] = schedule
		return nil
	})
	return err
}

func (s *Scheduler) Recover(ctx context.Context) error {
	state, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		for id, schedule := range state.Schedules {
			if schedule.PendingRun.IsZero() {
				continue
			}
			schedule.PendingRun = time.Time{}
			state.Schedules[id] = schedule
		}
		return nil
	})
	return err
}

func (s *Scheduler) Next(expression string, after time.Time) (time.Time, error) {
	cron, err := ParseCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	return cron.Next(after)
}
