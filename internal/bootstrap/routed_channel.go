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
// The chatID parameter every ports.Channel method already takes is
// superseded by ctx, not combined with it: routedChannel ignores whatever
// chatID a caller passes and resolves the real target itself (Telegram's
// configured owner chat ID, or the destination's web thread ID), so none of
// the existing tool constructors built with a fixed chatID baked in
// (calendarTools, skillProposeTool, the progress tracker) need to change.
type routedChannel struct {
	telegram       ports.Channel
	web            ports.Channel
	telegramChatID string
}

// newRoutedChannel returns telegram or web directly, unwrapped, if only one
// is configured (nil is acceptable for either), a routedChannel if both
// are, or noopChannel{} if neither is.
func newRoutedChannel(telegram, web ports.Channel, telegramChatID string) ports.Channel {
	switch {
	case telegram == nil && web == nil:
		return noopChannel{}
	case web == nil:
		return telegram
	case telegram == nil:
		return web
	default:
		return &routedChannel{telegram: telegram, web: web, telegramChatID: telegramChatID}
	}
}

// route resolves this turn's destination into the underlying channel to
// call and the real chatID to pass it -- never the chatID the caller
// passed in.
func (r *routedChannel) route(ctx context.Context) (channel ports.Channel, chatID string) {
	destination := approvals.DestinationFromContext(ctx)
	if destination.Kind == approvals.DestinationWeb {
		return r.web, destination.ThreadID
	}
	return r.telegram, r.telegramChatID
}

func (r *routedChannel) Deliver(ctx context.Context, _ string, text string) error {
	channel, chatID := r.route(ctx)
	return channel.Deliver(ctx, chatID, text)
}

func (r *routedChannel) DeliverApproval(ctx context.Context, _ string, approval approvals.Approval) error {
	channel, chatID := r.route(ctx)
	return channel.DeliverApproval(ctx, chatID, approval)
}

func (r *routedChannel) DeliverTrackable(ctx context.Context, _ string, text string) (string, error) {
	channel, chatID := r.route(ctx)
	return channel.DeliverTrackable(ctx, chatID, text)
}

func (r *routedChannel) EditText(ctx context.Context, _ string, messageID, text string) error {
	channel, chatID := r.route(ctx)
	return channel.EditText(ctx, chatID, messageID, text)
}

// AnswerCallback only ever reaches Telegram: a callbackQueryID is a
// Telegram button-tap concept webchat's AnswerCallback already treats as a
// no-op, and only Telegram ever produces one, so there is no destination
// ambiguity to resolve.
func (r *routedChannel) AnswerCallback(ctx context.Context, callbackQueryID string) error {
	return r.telegram.AnswerCallback(ctx, callbackQueryID)
}

func (r *routedChannel) SendTyping(ctx context.Context, _ string) error {
	channel, chatID := r.route(ctx)
	return channel.SendTyping(ctx, chatID)
}
