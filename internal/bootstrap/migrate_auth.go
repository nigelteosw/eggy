package bootstrap

import (
	"context"

	"github.com/nigelteosw/eggy/internal/ports"
)

// migrateCalendarAuth moves the Google Calendar credential written by an
// older Eggy out of state.json and into auth.json, where every provider
// credential now lives.
//
// It is idempotent and conservative: a credential already in auth.json wins,
// and the state.json copy is cleared only once the auth.json write has
// succeeded, so a crash between the two leaves the owner still authorized
// rather than silently logged out.
func migrateCalendarAuth(ctx context.Context, store ports.StateStore, auth ports.CalendarAuthStore) error {
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	legacy := state.Calendar
	if legacy == (ports.CalendarAuth{}) {
		return nil
	}
	current, err := auth.Load(ctx)
	if err != nil {
		return err
	}
	if current == (ports.CalendarAuth{}) {
		if err := auth.Update(ctx, func(target *ports.CalendarAuth) error {
			*target = legacy
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = store.Update(ctx, state.Version, func(state *ports.State) error {
		state.Calendar = ports.CalendarAuth{}
		return nil
	})
	return err
}
