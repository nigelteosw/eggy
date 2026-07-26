package bootstrap

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// routedChannel implements ports.Channel by reading the destination
// stamped on ctx for this turn (see internal/kernel/approvals/destination.go)
// and forwarding to exactly one underlying channel -- Telegram or web --
// rather than fanning every call out to both, since Telegram and the web UI
// are independent channels, never mirrors of one conversation. See
// docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md.
//
// It only chooses a channel. Each underlying channel resolves its own
// target: the Telegram client is bound to the owner chat at construction,
// and webchat reads the destination's thread ID off the same ctx.
type routedChannel struct {
	telegram ports.Channel
	web      ports.Channel
}

// newRoutedChannel returns telegram or web directly, unwrapped, if only one
// is configured (nil is acceptable for either), a routedChannel if both
// are, or noopChannel{} if neither is.
func newRoutedChannel(telegram, web ports.Channel) ports.Channel {
	switch {
	case telegram == nil && web == nil:
		return noopChannel{}
	case web == nil:
		return telegram
	case telegram == nil:
		return web
	default:
		return &routedChannel{telegram: telegram, web: web}
	}
}

// route resolves this turn's destination into the underlying channel to
// call.
func (r *routedChannel) route(ctx context.Context) ports.Channel {
	if approvals.DestinationFromContext(ctx).Kind == approvals.DestinationWeb {
		return r.web
	}
	return r.telegram
}

func (r *routedChannel) Deliver(ctx context.Context, text string) error {
	return r.route(ctx).Deliver(ctx, text)
}

func (r *routedChannel) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	return r.route(ctx).DeliverApproval(ctx, approval)
}

func (r *routedChannel) DeliverTrackable(ctx context.Context, text string) (string, error) {
	return r.route(ctx).DeliverTrackable(ctx, text)
}

func (r *routedChannel) EditText(ctx context.Context, messageID, text string) error {
	return r.route(ctx).EditText(ctx, messageID, text)
}

func (r *routedChannel) SendTyping(ctx context.Context) error {
	return r.route(ctx).SendTyping(ctx)
}
