// Package destination identifies which independent surface -- Telegram's
// single fixed thread, one web thread, or one Discord DM -- a turn's
// replies, approval decisions, and events should reach. Approvals, events,
// and the turn orchestrator are all consumers of Destination; none of them
// owns it.
package destination

import (
	"context"
	"errors"
	"fmt"
)

// Destination identifies which independent channel -- Telegram's single
// fixed thread, one web thread, or one Discord DM -- a turn's replies and
// approval decisions should reach. Each surface is an independent channel
// into the same agent core, never a mirror of one conversation: a
// Destination is how that routing decision travels through a turn.
//
// A destination names a place, never a person. Who is speaking comes from
// the Principal on ctx; a Discord channel ID in particular is opaque and
// carries no identity of its own.
type Destination struct {
	Kind string `json:"kind"`
	// ThreadID is only set when Kind == Web.
	ThreadID string `json:"thread_id,omitempty"`
	// ChannelID is only set when Kind == Discord: the DM channel the turn
	// arrived in and replies return to.
	ChannelID string `json:"channel_id,omitempty"`
}

const (
	Telegram = "telegram"
	Web      = "web"
	Discord  = "discord"
)

// telegramConversationID is the fixed, reserved SQLite conversation_id
// Telegram's single continuous thread always uses -- never returned by the
// web thread-listing query (WHERE channel = 'web').
const telegramConversationID = "telegram"

// ConversationID returns the SQLite conversation_id this destination's
// thread reads and writes: Telegram's fixed thread, a web thread's own
// generated ID, or one Discord DM's channel-keyed history.
//
// An empty Kind is the legacy personal default -- a record written before
// destinations were explicit -- and still means Telegram.
func (d Destination) ConversationID() string {
	switch d.Kind {
	case Web:
		return d.ThreadID
	case Discord:
		return "discord:dm:" + d.ChannelID
	}
	return telegramConversationID
}

// Validate refuses a destination no surface could deliver to: a Discord
// destination with no channel, or a kind nothing here knows. An unknown
// kind is an error rather than a Telegram fallback, because sending an
// owner's reply to the wrong surface is worse than not sending it.
func (d Destination) Validate() error {
	switch d.Kind {
	case "", Telegram, Web:
		return nil
	case Discord:
		if d.ChannelID == "" {
			return errors.New("discord destination has no channel")
		}
		return nil
	default:
		return fmt.Errorf("unsupported destination kind %q", d.Kind)
	}
}

type contextKey struct{}

// With attaches d to ctx for the rest of a turn. Every ports.Channel method
// already takes ctx as its first parameter, and every tool's Execute(ctx,
// ...) receives that same per-turn ctx, so this is the one place a turn's
// destination needs to be set.
func With(ctx context.Context, d Destination) context.Context {
	return context.WithValue(ctx, contextKey{}, d)
}

// FromContext returns the destination carried on ctx, defaulting to
// Telegram's fixed thread when none was set -- preserving existing behavior
// for callers (tests and scheduled turns) that never go
// through a web request's per-turn destination stamping.
func FromContext(ctx context.Context) Destination {
	if d, ok := ctx.Value(contextKey{}).(Destination); ok {
		return d
	}
	return Destination{Kind: Telegram}
}
