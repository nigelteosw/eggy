package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/scheduler/cronfile"
)

// migrateSchedules moves the schedules an older Eggy kept inside state.json
// into <home>/cron, one editable file per job.
//
// Like the calendar migration, every file is written before state.json is
// cleared, so a crash in the middle leaves a duplicate rather than a lost
// schedule -- and a job already present in cron/ wins, since that is the one
// the running scheduler has been updating.
func migrateSchedules(ctx context.Context, store ports.StateStore, cron *cronfile.Store) error {
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if len(state.Schedules) == 0 {
		return nil
	}
	for id, schedule := range state.Schedules {
		if _, err := cron.Get(id); err == nil {
			continue
		} else if !errors.Is(err, cronfile.ErrNotFound) {
			return err
		}
		schedule.ID = id
		if err := cron.Put(schedule); err != nil {
			return fmt.Errorf("write schedule %s: %w", id, err)
		}
	}
	_, err = store.Update(ctx, state.Version, func(state *ports.State) error {
		state.Schedules = nil
		return nil
	})
	return err
}
