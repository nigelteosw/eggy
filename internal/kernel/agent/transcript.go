package agent

import "context"

// Transcript is the durable record of one turn. Every turn has one, not only
// a turn that happens to be editing a repository: the loop is the same loop
// either way, so "what happened, in order" is worth keeping either way.
//
// It is deliberately a sink, not a store: the loop appends and checkpoints,
// and never reads back. Neither method returns an error, because a
// transcript that cannot be written is not a reason to fail the owner's
// turn -- the adapter owns logging its own failures.
type Transcript interface {
	// Append records one loop event: an assistant message, a tool start, or
	// a tool result (including a failed one).
	Append(ctx context.Context, event Event)
	// Checkpoint records that the live message window was compacted, with
	// the running summary that replaced the folded-away steps.
	Checkpoint(ctx context.Context, summary string)
}
