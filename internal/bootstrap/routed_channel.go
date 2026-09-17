package bootstrap

import (
	"context"
	"errors"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/channelutil"
)

// routedChannel implements ports.Channel by reading the destination
// stamped on ctx for this turn (see internal/kernel/destination)
// and forwarding to exactly one underlying channel -- Telegram, web, or
// Discord -- rather than fanning every call out to all of them, since each
// is an independent channel, never a mirror of one conversation. See
// docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md.
//
// It only chooses a channel. Each underlying channel resolves its own
// target: the Telegram client is bound to the owner chat at construction,
// and webchat and Discord read the thread or DM channel off the same ctx.
//
// Dispatch is by explicit supported kind. A destination the router cannot
// serve -- an unknown kind, a Discord DM with no channel ID, or a Discord
// target when no Discord surface is configured -- is an error, never a
// Telegram fallback: a reply sent to the wrong surface reaches the wrong
// person.
//
// It implements the optional ports.TrackableChannel, ports.TypingChannel and
// ports.ProgressChannel extensions unconditionally, because a Go type either has a method or it
// does not and the honest answer here ("trackable when this turn routes to
// a trackable channel") is not expressible statically. The capability check
// therefore moves inside each method, via the channelutil helpers, so a
// turn routed at a channel lacking the affordance degrades exactly as a
// direct caller on that channel would.
type routedChannel struct {
	telegram ports.Channel
	web      ports.Channel
	discord  ports.Channel
}

// newRoutedChannel returns telegram directly, unwrapped, when it is the only
// configured surface, a routedChannel when any other surface exists, or
// noopChannel{} if none is.
//
// A deployment without Telegram deliberately gets a routedChannel over a
// *noop* Telegram rather than another channel unwrapped. Unprompted output
// (scheduled messages) is addressed to Telegram by
// decision -- see proactiveDestination -- so unwrapping here would quietly
// redirect it into a web thread or DM the owner never asked to be pushed
// to. Dropping it instead keeps "no Telegram configured" meaning "no
// unprompted output", while owner-initiated web and Discord turns, which
// stamp their own destination, still route normally.
func newRoutedChannel(telegram, web, discord ports.Channel) ports.Channel {
	switch {
	case telegram == nil && web == nil && discord == nil:
		return noopChannel{}
	case web == nil && discord == nil:
		return telegram
	}
	if telegram == nil {
		telegram = noopChannel{}
	}
	return &routedChannel{telegram: telegram, web: web, discord: discord}
}

// route resolves this turn's destination into the underlying channel to
// call, or the reason it cannot be called.
func (r *routedChannel) route(ctx context.Context) (ports.Channel, error) {
	dest := destination.FromContext(ctx)
	if err := dest.Validate(); err != nil {
		return nil, err
	}
	switch dest.Kind {
	case destination.Web:
		if r.web == nil {
			return nil, errors.New("web channel is not configured")
		}
		return r.web, nil
	case destination.Discord:
		if r.discord == nil {
			return nil, errors.New("discord channel is not configured")
		}
		return r.discord, nil
	default:
		return r.telegram, nil
	}
}

func (r *routedChannel) Deliver(ctx context.Context, text string) error {
	channel, err := r.route(ctx)
	if err != nil {
		return err
	}
	return channel.Deliver(ctx, text)
}

func (r *routedChannel) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	channel, err := r.route(ctx)
	if err != nil {
		return err
	}
	return channel.DeliverApproval(ctx, approval)
}

func (r *routedChannel) DeliverTrackable(ctx context.Context, text string) (string, error) {
	channel, err := r.route(ctx)
	if err != nil {
		return "", err
	}
	return channelutil.DeliverTrackable(ctx, channel, text)
}

func (r *routedChannel) EditText(ctx context.Context, messageID, text string) error {
	channel, err := r.route(ctx)
	if err != nil {
		return err
	}
	return channelutil.EditText(ctx, channel, messageID, text)
}

func (r *routedChannel) ShowProgress(ctx context.Context, text string) error {
	channel, err := r.route(ctx)
	if err != nil {
		return err
	}
	return channelutil.ShowProgress(ctx, channel, text)
}

func (r *routedChannel) SendTyping(ctx context.Context) error {
	channel, err := r.route(ctx)
	if err != nil {
		return err
	}
	typing, ok := channel.(ports.TypingChannel)
	if !ok {
		return nil
	}
	return typing.SendTyping(ctx)
}
