package webchat

import (
	"context"

	"github.com/nigelteosw/eggy/internal/kernel/approvals"
)

// Channel implements ports.Channel over a Hub. It is a browser chat
// surface, not a Telegram-style bot API: each call scopes its broadcast to
// the one thread the turn is running in, read from the destination stamped
// on ctx. A turn carrying no web destination has no thread to broadcast to,
// so the call is dropped rather than fanned out to every open connection.
type Channel struct {
	hub *Hub
}

func New(hub *Hub) *Channel {
	return &Channel{hub: hub}
}

// thread returns this turn's destination thread, and false when ctx does
// not carry one.
func (c *Channel) thread(ctx context.Context) (string, bool) {
	destination := approvals.DestinationFromContext(ctx)
	if destination.Kind != approvals.DestinationWeb || destination.ThreadID == "" {
		return "", false
	}
	return destination.ThreadID, true
}

func (c *Channel) Deliver(ctx context.Context, text string) error {
	if threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(threadID, Event{Kind: EventMessage, ID: c.hub.NextMessageID(), Text: text})
	}
	return nil
}

func (c *Channel) DeliverTrackable(ctx context.Context, text string) (string, error) {
	threadID, ok := c.thread(ctx)
	if !ok {
		return "", nil
	}
	id := c.hub.NextMessageID()
	c.hub.Broadcast(threadID, Event{Kind: EventMessage, ID: id, Text: text})
	return id, nil
}

func (c *Channel) EditText(ctx context.Context, messageID string, text string) error {
	if threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(threadID, Event{Kind: EventEdit, ID: messageID, Text: text})
	}
	return nil
}

func (c *Channel) SendTyping(ctx context.Context) error {
	if threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(threadID, Event{Kind: EventTyping})
	}
	return nil
}

func (c *Channel) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	if threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(threadID, Event{Kind: EventApproval, Approval: &ApprovalPayload{ID: approval.ID, Summary: approval.Summary}})
	}
	return nil
}
