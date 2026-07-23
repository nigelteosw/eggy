package bootstrap

import (
	"context"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
	"github.com/nigelteosw/eggy/internal/ports"
)

// multiChannel implements ports.Channel by fanning every call out to both
// Telegram and the web chat channel, so the same conversation is observable
// live from both surfaces. See docs/superpowers/specs/2026-07-23-web-chat-interface-design.md
// for the compound trackable-message-ID scheme EditText/DeliverTrackable use.
type multiChannel struct {
	telegram ports.Channel
	web      ports.Channel
}

// newMultiChannel returns telegram or web directly, unwrapped, if only one
// is configured (nil is acceptable for either), a multiChannel if both are,
// or noopChannel{} if neither is.
func newMultiChannel(telegram, web ports.Channel) ports.Channel {
	switch {
	case telegram == nil && web == nil:
		return noopChannel{}
	case web == nil:
		return telegram
	case telegram == nil:
		return web
	default:
		return &multiChannel{telegram: telegram, web: web}
	}
}

func (m *multiChannel) Deliver(ctx context.Context, chatID, text string) error {
	errTelegram := m.telegram.Deliver(ctx, chatID, text)
	errWeb := m.web.Deliver(ctx, chatID, text)
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

func (m *multiChannel) DeliverApproval(ctx context.Context, chatID string, approval approvals.Approval) error {
	errTelegram := m.telegram.DeliverApproval(ctx, chatID, approval)
	errWeb := m.web.DeliverApproval(ctx, chatID, approval)
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

func (m *multiChannel) DeliverTrackable(ctx context.Context, chatID, text string) (string, error) {
	telegramID, errTelegram := m.telegram.DeliverTrackable(ctx, chatID, text)
	webID, errWeb := m.web.DeliverTrackable(ctx, chatID, text)
	var parts []string
	if errTelegram == nil {
		parts = append(parts, "telegram:"+telegramID)
	}
	if errWeb == nil {
		parts = append(parts, "web:"+webID)
	}
	if len(parts) == 0 {
		return "", errors.Join(errTelegram, errWeb)
	}
	return strings.Join(parts, "|"), nil
}

func (m *multiChannel) EditText(ctx context.Context, chatID, messageID, text string) error {
	var errTelegram, errWeb error
	recognized := false
	for _, part := range strings.Split(messageID, "|") {
		channel, id, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		switch channel {
		case "telegram":
			recognized = true
			errTelegram = m.telegram.EditText(ctx, chatID, id, text)
		case "web":
			recognized = true
			errWeb = m.web.EditText(ctx, chatID, id, text)
		}
	}
	if !recognized {
		// messageID predates the compound-ID scheme: Telegram's own
		// approval-button callbacks report callback_query.message.message_id
		// directly (approval messages are sent via DeliverApproval, which
		// returns no ID at all, so they never round-trip through
		// DeliverTrackable to pick up a "telegram:"/"web:" prefix). Treat an
		// unrecognized, unprefixed ID as a raw Telegram message ID.
		return m.telegram.EditText(ctx, chatID, messageID, text)
	}
	if errTelegram != nil && errWeb != nil {
		return errors.Join(errTelegram, errWeb)
	}
	return nil
}

// AnswerCallback only ever reaches Telegram: a callbackQueryID is a
// Telegram button-tap concept webchat's AnswerCallback already treats as a
// no-op, so there is nothing useful to fan out to on the web side.
func (m *multiChannel) AnswerCallback(ctx context.Context, callbackQueryID string) error {
	return m.telegram.AnswerCallback(ctx, callbackQueryID)
}

func (m *multiChannel) SendTyping(ctx context.Context, chatID string) error {
	_ = m.web.SendTyping(ctx, chatID)
	return m.telegram.SendTyping(ctx, chatID)
}
