package webchat

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// Channel implements ports.Channel over a Hub. It is a browser chat
// surface, not a Telegram-style bot API: chatID is the target thread ID
// (resolved by bootstrap's routedChannel from the turn's ctx-carried
// destination, not whatever a tool constructor was built with), so every
// call scopes its broadcast to that one thread's open connections.
type Channel struct {
	hub *Hub
}

func New(hub *Hub) *Channel {
	return &Channel{hub: hub}
}

func (c *Channel) Deliver(_ context.Context, threadID string, text string) error {
	c.hub.Broadcast(threadID, Event{Kind: EventMessage, ID: c.hub.NextMessageID(), Text: text})
	return nil
}

func (c *Channel) DeliverTrackable(_ context.Context, threadID string, text string) (string, error) {
	id := c.hub.NextMessageID()
	c.hub.Broadcast(threadID, Event{Kind: EventMessage, ID: id, Text: text})
	return id, nil
}

func (c *Channel) EditText(_ context.Context, threadID string, messageID string, text string) error {
	c.hub.Broadcast(threadID, Event{Kind: EventEdit, ID: messageID, Text: text})
	return nil
}

func (c *Channel) SendTyping(_ context.Context, threadID string) error {
	c.hub.Broadcast(threadID, Event{Kind: EventTyping})
	return nil
}

func (c *Channel) DeliverApproval(_ context.Context, threadID string, approval approvals.Approval) error {
	c.hub.Broadcast(threadID, Event{Kind: EventApproval, Approval: &ApprovalPayload{ID: approval.ID, Summary: approval.Summary}})
	return nil
}

// AnswerCallback is a no-op: "answering a callback query" is a Telegram
// button-tap concept with no browser equivalent.
func (c *Channel) AnswerCallback(context.Context, string) error {
	return nil
}
