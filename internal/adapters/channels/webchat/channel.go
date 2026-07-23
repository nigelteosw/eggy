package webchat

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// Channel implements ports.Channel over a Hub. It is a browser chat surface,
// not a Telegram-style bot API: chatID is accepted (to satisfy the
// interface) but ignored for routing -- there is exactly one owner and one
// conversation, so every call broadcasts to every open connection.
type Channel struct {
	hub *Hub
}

func New(hub *Hub) *Channel {
	return &Channel{hub: hub}
}

func (c *Channel) Deliver(_ context.Context, _ string, text string) error {
	c.hub.Broadcast(Event{Kind: EventMessage, ID: c.hub.NextMessageID(), Text: text})
	return nil
}

func (c *Channel) DeliverTrackable(_ context.Context, _ string, text string) (string, error) {
	id := c.hub.NextMessageID()
	c.hub.Broadcast(Event{Kind: EventMessage, ID: id, Text: text})
	return id, nil
}

func (c *Channel) EditText(_ context.Context, _ string, messageID string, text string) error {
	c.hub.Broadcast(Event{Kind: EventEdit, ID: messageID, Text: text})
	return nil
}

func (c *Channel) SendTyping(_ context.Context, _ string) error {
	c.hub.Broadcast(Event{Kind: EventTyping})
	return nil
}

func (c *Channel) DeliverApproval(_ context.Context, _ string, approval approvals.Approval) error {
	c.hub.Broadcast(Event{Kind: EventApproval, Approval: &ApprovalPayload{ID: approval.ID, Summary: approval.Summary}})
	return nil
}

// AnswerCallback is a no-op: "answering a callback query" is a Telegram
// button-tap concept with no browser equivalent.
func (c *Channel) AnswerCallback(context.Context, string) error {
	return nil
}
