package approvals

import "context"

// Destination identifies which independent channel -- Telegram's single
// fixed thread, or one web thread -- a turn's replies and approval
// decisions should reach. Telegram and the web UI are independent
// channels into the same agent core, never mirrors of one conversation
// (see docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md):
// a Destination is how that routing decision travels through a turn.
type Destination struct {
	Kind string `json:"kind"`
	// ThreadID is only set when Kind == DestinationWeb.
	ThreadID string `json:"thread_id,omitempty"`
}

const (
	DestinationTelegram = "telegram"
	DestinationWeb      = "web"
)

// telegramConversationID is the fixed, reserved SQLite conversation_id
// Telegram's single continuous thread always uses -- never returned by the
// web thread-listing query (WHERE channel = 'web').
const telegramConversationID = "telegram"

// ConversationID returns the SQLite conversation_id this destination's
// thread reads and writes: Telegram's fixed thread, or a web thread's own
// generated ID.
func (d Destination) ConversationID() string {
	if d.Kind == DestinationWeb {
		return d.ThreadID
	}
	return telegramConversationID
}

type destinationContextKey struct{}

// WithDestination attaches d to ctx for the rest of a turn. Every
// ports.Channel method already takes ctx as its first parameter, and every
// tool's Execute(ctx, ...) receives that same per-turn ctx, so this is the
// one place a turn's destination needs to be set.
func WithDestination(ctx context.Context, d Destination) context.Context {
	return context.WithValue(ctx, destinationContextKey{}, d)
}

// DestinationFromContext returns the destination carried on ctx, defaulting
// to Telegram's fixed thread when none was set -- preserving existing
// behavior for callers (tests, the CLI, scheduled/heartbeat turns) that
// never go through a web request's per-turn destination stamping.
func DestinationFromContext(ctx context.Context) Destination {
	if d, ok := ctx.Value(destinationContextKey{}).(Destination); ok {
		return d
	}
	return Destination{Kind: DestinationTelegram}
}
