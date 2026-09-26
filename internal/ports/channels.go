package ports

import (
	"context"

	"github.com/nigelteosw/eggy/internal/core/approvals"
)

// Channel delivers agent output to one surface. It deliberately carries no
// chat or thread identifier: the target is the destination stamped on ctx
// for the turn (see internal/core/destination.Destination), so a tool or
// helper constructed once at startup reports into whichever conversation is
// actually running rather than into a fixed one baked in at construction.
//
// The port covers delivery only. Acknowledging a Telegram callback query is
// part of *receiving* an update and lives in that adapter's webhook handler,
// not here, so no other surface has to implement a concept it doesn't have.
//
// Channel is the floor every surface must reach: send text, and ask for a
// decision. In-place edits and typing indicators are surface-specific
// affordances, so they live in the optional TrackableChannel and
// TypingChannel extensions rather than forcing a surface without them to
// stub methods it cannot honour. Consumers type-assert for the extension
// they want and degrade when it is absent -- see internal/channel/
// channelutil, which does exactly that once so callers don't repeat it.
type Channel interface {
	Deliver(ctx context.Context, text string) error
	DeliverApproval(ctx context.Context, approval approvals.Approval) error
}

// TrackableChannel is a Channel whose messages can be revised after the
// fact: DeliverTrackable returns a handle for the message it sent, and
// EditText rewrites that message in place. Surfaces use it to keep one live
// message for a long-running run instead of a message per step.
type TrackableChannel interface {
	Channel
	DeliverTrackable(ctx context.Context, text string) (messageID string, err error)
	EditText(ctx context.Context, messageID, text string) error
}

// TypingChannel is a Channel that can show the user that a turn is in
// progress. The indicator is advisory: a surface without one is still a
// perfectly good Channel.
type TypingChannel interface {
	Channel
	SendTyping(ctx context.Context) error
}

// ProgressChannel is a Channel that can show what a turn is doing right now
// -- "Calling web_search..." -- without that status becoming a message in
// the conversation. A surface without it gets the same status as a
// trackable message instead (see channelutil.ShowProgress), so the
// extension only changes where the status is drawn, never whether it is.
type ProgressChannel interface {
	Channel
	ShowProgress(ctx context.Context, text string) error
}
