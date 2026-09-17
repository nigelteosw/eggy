package discord

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// Client is the DiscordGo-backed Transport plus the gateway lifecycle. It
// is the only file that imports the SDK.
type Client struct {
	session *discordgo.Session
	logger  *slog.Logger
	// remove unregisters the message handler; closeOnce makes Close
	// idempotent so a restart that closes the old transport after the drain
	// cannot close it twice.
	remove    func()
	closeOnce sync.Once
	closeErr  error
}

func NewClient(token string, logger *slog.Logger) (*Client, error) {
	if token == "" {
		return nil, errors.New("discord bot token is empty")
	}
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	// Least required intents: DMs only. Message content in DMs needs no
	// privileged intent, and nothing here reads guilds or members.
	session.Identify.Intents = discordgo.IntentsDirectMessages
	// No SDK state cache: it would retain every message body the gateway
	// delivers, including the ones intake refuses. The handler keeps the
	// one fact it needs (which channels are DMs) itself.
	session.StateEnabled = false
	session.State.MaxMessageCount = 0
	session.State.TrackChannels = false
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{session: session, logger: logger}, nil
}

// Open connects the gateway and routes every message event to handle. The
// context passed to handle is the daemon's, not the gateway's: a turn
// outlives the websocket frame that started it.
func (c *Client) Open(ctx context.Context, handle func(context.Context, Inbound)) error {
	c.remove = c.session.AddHandler(func(_ *discordgo.Session, event *discordgo.MessageCreate) {
		if event == nil || event.Message == nil {
			return
		}
		handle(ctx, inbound(event.Message))
	})
	return c.session.Open()
}

// Close stops intake and closes the gateway exactly once.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.remove != nil {
			c.remove()
		}
		c.closeErr = c.session.Close()
	})
	return c.closeErr
}

// inbound maps the SDK message onto the adapter's own shape.
func inbound(m *discordgo.Message) Inbound {
	in := Inbound{MessageID: m.ID, ChannelID: m.ChannelID, GuildID: m.GuildID, WebhookID: m.WebhookID, Content: m.Content, Attachments: len(m.Attachments)}
	if m.Author != nil {
		in.AuthorID, in.AuthorIsBot = m.Author.ID, m.Author.Bot
	}
	if ref := m.ReferencedMessage; ref != nil {
		reference := &Reference{Content: ref.Content}
		if ref.Author != nil {
			reference.AuthorID, reference.AuthorIsBot = ref.Author.ID, ref.Author.Bot
		}
		in.Reference = reference
	}
	return in
}

// noMentions disables mention parsing on every send and edit: model output
// that happens to contain @everyone or a user mention must never ping.
var noMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}

func (c *Client) SendMessage(ctx context.Context, channelID, content string) (string, error) {
	message, err := c.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content, AllowedMentions: noMentions}, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return message.ID, nil
}

func (c *Client) EditMessage(ctx context.Context, channelID, messageID, content string) error {
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: messageID, Channel: channelID, Content: &content, AllowedMentions: noMentions}, discordgo.WithContext(ctx))
	return err
}

func (c *Client) Typing(ctx context.Context, channelID string) error {
	return c.session.ChannelTyping(channelID, discordgo.WithContext(ctx))
}

func (c *Client) DMChannel(ctx context.Context, channelID string) (DMInfo, error) {
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx))
	if err != nil {
		return DMInfo{}, err
	}
	if channel.Type != discordgo.ChannelTypeDM || len(channel.Recipients) != 1 || channel.Recipients[0] == nil {
		return DMInfo{}, nil
	}
	return DMInfo{OneToOne: true, RecipientID: channel.Recipients[0].ID}, nil
}
