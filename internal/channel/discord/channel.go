package discord

import (
	"context"
	"errors"
	"strings"

	"github.com/nigelteosw/eggy/internal/core/approvals"
	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Discord messages can be edited in place and the channel has a typing
// trigger, so the channel honours both optional extensions.
var (
	_ ports.TrackableChannel = (*Channel)(nil)
	_ ports.TypingChannel    = (*Channel)(nil)
)

// maxMessageLength is Discord's content limit per message.
const maxMessageLength = 2000

// RecipientResolver maps the acting account to its linked Discord user,
// against live config: an account whose Discord was unlinked mid-turn
// resolves to nothing, and nothing is where its output goes.
type RecipientResolver func(accountID string) (userID string, ok bool)

// ErrNoRecipient reports a delivery for an account with no Discord, or
// into a DM that is not that account's own.
var ErrNoRecipient = errors.New("no Discord DM for this account")

// Channel implements ports.Channel into the DM stamped on ctx. Every call
// re-checks that the DM belongs to the acting principal's currently linked
// user, so revoking a link stops delivery for work already in flight.
type Channel struct {
	transport  Transport
	recipients RecipientResolver
	// panelURL is where approvals are decided; the DM only points there.
	panelURL string
	handler  *Handler
}

func NewChannel(transport Transport, recipients RecipientResolver, panelURL string, handler *Handler) *Channel {
	return &Channel{transport: transport, recipients: recipients, panelURL: strings.TrimRight(panelURL, "/"), handler: handler}
}

// channelID is the one place a call learns which DM it is for, and whether
// it may still write there.
func (c *Channel) channelID(ctx context.Context) (string, error) {
	dest := destination.FromContext(ctx)
	if dest.Kind != destination.Discord || dest.ChannelID == "" {
		return "", ErrNoRecipient
	}
	principal, err := ports.PrincipalFromContext(ctx)
	if err != nil {
		return "", ErrNoRecipient
	}
	userID, ok := c.recipients(principal.AccountID)
	if !ok || userID == "" {
		return "", ErrNoRecipient
	}
	info, err := c.handler.dm(ctx, dest.ChannelID)
	if err != nil {
		return "", err
	}
	if !info.OneToOne || info.RecipientID != userID {
		return "", errNotADM
	}
	return dest.ChannelID, nil
}

func (c *Channel) Deliver(ctx context.Context, text string) error {
	_, err := c.DeliverTrackable(ctx, text)
	return err
}

func (c *Channel) DeliverTrackable(ctx context.Context, text string) (string, error) {
	channelID, err := c.channelID(ctx)
	if err != nil {
		return "", err
	}
	var messageID string
	for _, chunk := range splitMessage(text) {
		id, err := c.transport.SendMessage(ctx, channelID, chunk)
		if err != nil {
			return messageID, err
		}
		messageID = id
	}
	return messageID, nil
}

func (c *Channel) EditText(ctx context.Context, messageID, text string) error {
	channelID, err := c.channelID(ctx)
	if err != nil {
		return err
	}
	chunks := splitMessage(text)
	if err := c.transport.EditMessage(ctx, channelID, messageID, chunks[0]); err != nil {
		return err
	}
	// Text that outgrew one message continues in new ones; the edited
	// message keeps the head.
	for _, chunk := range chunks[1:] {
		if _, err := c.transport.SendMessage(ctx, channelID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *Channel) SendTyping(ctx context.Context) error {
	channelID, err := c.channelID(ctx)
	if err != nil {
		return err
	}
	return c.transport.Typing(ctx, channelID)
}

// DeliverApproval is a notice, not a decision surface: the pending record
// already exists, and the owner decides in the authenticated panel.
func (c *Channel) DeliverApproval(ctx context.Context, approval approvals.Approval) error {
	text := "**Approval needed**\n" + approval.Summary + "\n\nDecide it in the Eggy web panel"
	if c.panelURL != "" {
		text += ": " + c.panelURL
	}
	return c.Deliver(ctx, text+"\nApprovals can't be given here.")
}

// splitMessage cuts text into Discord-sized chunks at line boundaries where
// it can, so a long reply arrives as several readable messages rather than
// one refused request. Empty text still yields one (empty) chunk so an
// edit to nothing is expressible.
func splitMessage(text string) []string {
	if len(text) <= maxMessageLength {
		return []string{text}
	}
	var chunks []string
	for len(text) > maxMessageLength {
		cut := strings.LastIndex(text[:maxMessageLength], "\n")
		if cut < maxMessageLength/2 {
			cut = maxMessageLength
		}
		chunks = append(chunks, strings.TrimRight(text[:cut], "\n"))
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
