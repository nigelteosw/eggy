package services

import "context"

type unpromptedKey struct{}

// WithUnpromptedTurn marks ctx as belonging to a turn nobody asked for: a
// scheduled agent turn or a heartbeat. It travels on ctx the same way the
// transcript and destination do, so a tool deep in the turn
// (propose_change -> Ship) can hold the unprompted-turn invariant without
// every intermediate signature carrying a flag.
//
// The invariant it carries is narrow and deliberate. An unprompted turn may
// write to a repository and propose the result, because nothing lands
// without a payload-bound authorization and a human-reviewed pull request
// either way. What it may not do is present that work as finished: its pull
// request is always a draft, and always on a branch of its own.
func WithUnpromptedTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, unpromptedKey{}, true)
}

// IsUnpromptedTurn reports whether this turn is unprompted. The zero value is
// the safe direction on purpose: an owner-prompted turn is the default, and a
// caller that forgets to mark an unprompted one is caught by the run-options
// allowlist rather than by silently gaining owner privileges -- see
// proposeOnlyRunOptions in internal/bootstrap.
func IsUnpromptedTurn(ctx context.Context) bool {
	unprompted, _ := ctx.Value(unpromptedKey{}).(bool)
	return unprompted
}
