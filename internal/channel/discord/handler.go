// Package discord is the personal Discord channel: one bot, private DMs
// with linked owners, replies back into the same DM. Provider types stay
// inside this package -- the rest of Eggy sees events.Event and
// ports.Channel -- and the gateway/REST lifecycle sits behind a narrow
// Transport so intake and delivery are tested without a Discord session.
package discord

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/core/destination"
	"github.com/nigelteosw/eggy/internal/core/events"
)

// Source is the event source and conversation channel name Discord turns
// record under.
const Source = "discord"

// Inbound is one gateway message in the shape intake needs, mapped by the
// client from the SDK's message so the handler never imports it.
type Inbound struct {
	MessageID string
	ChannelID string
	// GuildID is set for anything posted in a server; a DM has none.
	GuildID     string
	AuthorID    string
	AuthorIsBot bool
	// WebhookID is set when a webhook, not a person, posted the message.
	WebhookID   string
	Content     string
	Attachments int
	// Reference is the message this one replies to, as Discord attached it
	// to the event: an immediate same-DM reference, never a history fetch.
	Reference *Reference
}

// Reference is the replied-to message.
type Reference struct {
	AuthorID    string
	AuthorIsBot bool
	Content     string
}

// DMInfo is what the adapter needs to know about a channel: whether it is
// a one-to-one DM and with whom. Group DMs and guild channels are not.
type DMInfo struct {
	OneToOne    bool
	RecipientID string
}

// Transport is the SDK surface the adapter uses. *Client implements it
// over DiscordGo; tests implement it in memory.
type Transport interface {
	SendMessage(ctx context.Context, channelID, content string) (messageID string, err error)
	EditMessage(ctx context.Context, channelID, messageID, content string) error
	Typing(ctx context.Context, channelID string) error
	// DMChannel describes the channel. It is the one REST lookup intake
	// makes, and only once per channel: the verdict is cached.
	DMChannel(ctx context.Context, channelID string) (DMInfo, error)
}

// EventSink hands a verified owner message to the dispatcher.
type EventSink func(context.Context, events.Event) error

// UserResolver maps a verified Discord user to the account it speaks for,
// against the live account directory: an unlinked user resolves to nothing.
type UserResolver func(userID string) (accountID string, ok bool)

// LinkConsumer redeems "/link <token>" for the user who sent it.
type LinkConsumer func(ctx context.Context, payload, userID string) error

// maxQuoteLength bounds the replied-to passage carried into the prompt.
const maxQuoteLength = 2000

// denialWindow is how often an unknown user is told they are unknown. One
// notice per window per user: enough to explain, too few to be a relay.
const denialWindow = 10 * time.Minute

// Handler is intake: it decides whether a gateway message is an owner's
// private DM and, if so, emits exactly one event for it. Everything else
// does no agent work.
type Handler struct {
	resolve   UserResolver
	sink      EventSink
	transport Transport
	link      LinkConsumer
	now       func() time.Time
	logger    *slog.Logger

	mu       sync.Mutex
	channels map[string]DMInfo
	denied   map[string]time.Time
}

func NewHandler(resolve UserResolver, sink EventSink, transport Transport, now func() time.Time, logger *slog.Logger) *Handler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{resolve: resolve, sink: sink, transport: transport, now: now, logger: logger, channels: map[string]DMInfo{}, denied: map[string]time.Time{}}
}

func (h *Handler) WithLinkConsumer(consume LinkConsumer) *Handler {
	h.link = consume
	return h
}

// dm resolves and caches whether the channel is a one-to-one DM with the
// author. A channel's kind never changes, so the verdict is cached for the
// process lifetime; only the boolean and recipient are kept, never bodies.
func (h *Handler) dm(ctx context.Context, channelID string) (DMInfo, error) {
	h.mu.Lock()
	info, ok := h.channels[channelID]
	h.mu.Unlock()
	if ok {
		return info, nil
	}
	info, err := h.transport.DMChannel(ctx, channelID)
	if err != nil {
		return DMInfo{}, err
	}
	h.mu.Lock()
	h.channels[channelID] = info
	h.mu.Unlock()
	return info, nil
}

// shouldDeny reports whether an unknown user gets a notice now, and records
// that they did. The map is pruned as it is consulted so it cannot grow
// with every stranger who ever wrote in.
func (h *Handler) shouldDeny(userID string) bool {
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, at := range h.denied {
		if now.Sub(at) >= denialWindow {
			delete(h.denied, id)
		}
	}
	if at, seen := h.denied[userID]; seen && now.Sub(at) < denialWindow {
		return false
	}
	h.denied[userID] = now
	return true
}

// Handle processes one gateway message. It returns an error only for a
// failure worth logging (a sink or transport error); every refusal is a
// nil return with no work done.
func (h *Handler) Handle(ctx context.Context, in Inbound) error {
	// Bots and webhooks never enter, and are never answered: a reply to a
	// bot is the start of a loop.
	if in.AuthorIsBot || in.WebhookID != "" || in.AuthorID == "" {
		return nil
	}
	// Anything in a server is ignored outright, owner or not: this
	// milestone is private DMs only.
	if in.GuildID != "" {
		return nil
	}
	info, err := h.dm(ctx, in.ChannelID)
	if err != nil {
		return err
	}
	if !info.OneToOne || info.RecipientID != in.AuthorID {
		return nil
	}
	text := strings.TrimSpace(in.Content)
	accountID, linked := h.resolve(in.AuthorID)
	if !linked {
		return h.handleUnlinked(ctx, in, text)
	}
	if isLinkCommand(text) {
		return h.notice(ctx, in.ChannelID, "This Discord is already linked to an Eggy account.")
	}
	if isDecisionCommand(text) {
		return h.notice(ctx, in.ChannelID, "Approvals are decided in the Eggy web panel, not here.")
	}
	if in.Attachments > 0 {
		return h.notice(ctx, in.ChannelID, "Attachments aren't supported on Discord yet; send text only.")
	}
	if text == "" {
		return nil
	}
	message := events.Message{Text: text, Quote: quote(in.Reference)}
	payload, _ := json.Marshal(message)
	event := events.Event{
		ID: "discord:message:" + in.MessageID, Type: events.TypeMessage, Source: Source, Owner: accountID,
		Timestamp: h.now().UTC(), CorrelationID: "discord:message:" + in.MessageID,
		Destination: destination.Destination{Kind: destination.Discord, ChannelID: in.ChannelID},
		Payload:     payload,
	}
	return h.sink(ctx, event)
}

// handleUnlinked is the only path an unknown user has: redeem a linking
// token, or be told once that they are unknown. Nothing they write is
// logged or kept.
func (h *Handler) handleUnlinked(ctx context.Context, in Inbound, text string) error {
	if isLinkCommand(text) && h.link != nil {
		token := strings.Fields(text)[1]
		if err := h.link(ctx, token, in.AuthorID); err != nil {
			return h.notice(ctx, in.ChannelID, "That linking token is invalid or expired. Create a new one in the Eggy web panel.")
		}
		return h.notice(ctx, in.ChannelID, "Linked. You can talk to Eggy here now.")
	}
	if !h.shouldDeny(in.AuthorID) {
		return nil
	}
	return h.notice(ctx, in.ChannelID, "This Eggy doesn't know you. If you have an account, link this Discord from the web panel.")
}

// notice sends a deterministic reply. A failed notice is logged, not
// returned: nothing upstream can do anything about it, and it must never
// be retried by re-running intake.
func (h *Handler) notice(ctx context.Context, channelID, text string) error {
	if _, err := h.transport.SendMessage(ctx, channelID, text); err != nil {
		h.logger.Warn("discord notice failed", "channel", channelID, "error", err)
	}
	return nil
}

func isLinkCommand(text string) bool {
	parts := strings.Fields(text)
	return len(parts) == 2 && parts[0] == "/link"
}

func isDecisionCommand(text string) bool {
	first := strings.ToLower(strings.TrimSpace(strings.SplitN(text, " ", 2)[0]))
	return first == "/approve" || first == "/reject"
}

// quote maps a Discord reply onto the surface-neutral events.Quote. In a
// one-to-one DM the only bot is Eggy, so a bot-authored reference is Eggy's
// own words. The passage is bounded: it is disambiguation, not history.
func quote(reference *Reference) *events.Quote {
	if reference == nil || strings.TrimSpace(reference.Content) == "" {
		return nil
	}
	text := reference.Content
	if len(text) > maxQuoteLength {
		text = text[:maxQuoteLength]
	}
	return &events.Quote{Text: text, OwnMessage: reference.AuthorIsBot}
}

var errNotADM = errors.New("discord destination is not this owner's DM")
