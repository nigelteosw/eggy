package webchat

import (
	"context"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// The browser surface renders edits, a typing indicator and a progress line
// as its own SSE event kinds, so it honours all three optional extensions.
var (
	_ ports.TrackableChannel = (*Channel)(nil)
	_ ports.TypingChannel    = (*Channel)(nil)
	_ ports.ProgressChannel  = (*Channel)(nil)
	_ ports.ImageChannel     = (*Channel)(nil)
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

// thread returns this turn's account and destination thread, and false when
// ctx does not carry both. A turn with no principal has no thread it may
// broadcast to: the account is half of the key, and there is no default.
func (c *Channel) thread(ctx context.Context) (string, string, bool) {
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", "", false
	}
	dest := destination.FromContext(ctx)
	if dest.Kind != destination.Web || dest.ThreadID == "" {
		return "", "", false
	}
	return principal.AccountID, dest.ThreadID, true
}

func (c *Channel) Deliver(ctx context.Context, text string) error {
	if accountID, threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(accountID, threadID, Event{Kind: EventMessage, ID: c.hub.NextMessageID(), Text: text})
	}
	return nil
}

// imageNotKept explains an image the browser cannot draw: it shows images by
// fetching them from the panel, and one made with tracing off was never stored.
const imageNotKept = "I made an image, but it couldn't be kept because tracing is off, so it can't be shown here. Turn tracing on in Settings to keep images."

// DeliverImage sends the image's stored reference, which the browser draws by
// fetching it -- the same marker the conversation record holds, so a reload
// shows exactly what arrived live. The bytes never travel over the stream.
func (c *Channel) DeliverImage(ctx context.Context, id string, _ ports.ContentPart) error {
	if id == "" {
		return c.Deliver(ctx, imageNotKept)
	}
	return c.Deliver(ctx, ports.ImageMarker(id))
}

func (c *Channel) DeliverTrackable(ctx context.Context, text string) (string, error) {
	accountID, threadID, ok := c.thread(ctx)
	if !ok {
		return "", nil
	}
	id := c.hub.NextMessageID()
	c.hub.Broadcast(accountID, threadID, Event{Kind: EventMessage, ID: id, Text: text})
	return id, nil
}

func (c *Channel) EditText(ctx context.Context, messageID string, text string) error {
	if accountID, threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(accountID, threadID, Event{Kind: EventEdit, ID: messageID, Text: text})
	}
	return nil
}

func (c *Channel) SendTyping(ctx context.Context) error {
	if accountID, threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(accountID, threadID, Event{Kind: EventTyping})
	}
	return nil
}

func (c *Channel) ShowProgress(ctx context.Context, text string) error {
	if accountID, threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(accountID, threadID, Event{Kind: EventProgress, Text: text})
	}
	return nil
}

func (c *Channel) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	if accountID, threadID, ok := c.thread(ctx); ok {
		c.hub.Broadcast(accountID, threadID, Event{Kind: EventApproval, Approval: &ApprovalPayload{ID: approval.ID, Summary: approval.Summary, Details: approvalDetails(approval.Payload)}})
	}
	return nil
}
