package bootstrap

import (
	"context"
	"log/slog"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/internal/web"
	"github.com/nigelteosw/eggy/plugins/auth/connections"
	"github.com/nigelteosw/eggy/plugins/channels/discord"
)

// discordWiring is everything the Discord surface contributes to an App:
// one transport, its intake handler, and the channel replies go through.
// Discord is optional, and its absence is this struct's zero value; every
// method is safe on it. The gateway is opened by Run and closed by it once,
// after in-flight turns drain, so a restart never loses a reply mid-turn.
type discordWiring struct {
	client  discordTransport
	handler *discord.Handler
	// channel is a separate interface field assigned only when a client
	// exists, so it stays a true nil for newRoutedChannel.
	channel ports.Channel
}

// discordTransport is what newDiscordWiring builds the surface over. In
// production it is the DiscordGo client; tests hand in a fake so the whole
// wiring runs without a gateway.
type discordTransport interface {
	discord.Transport
	Open(ctx context.Context, handle func(context.Context, discord.Inbound)) error
	Close() error
}

func newDiscordWiring(cfg config.Config, secrets config.Secrets, options AppOptions, accounts web.AccountDirectory, credentials *connections.Store, sink discord.EventSink, links *identityLinkCoordinator) discordWiring {
	if !cfg.DiscordEnabled() {
		return discordWiring{}
	}
	if options.discordTransport != nil {
		wiring := buildDiscordWiring(cfg, options, accounts, options.discordTransport, sink, links)
		wiring.client = options.discordTransport
		return wiring
	}
	if options.FakeAdapters {
		return discordWiring{}
	}
	token := discordBotToken(secrets, credentials)
	if token == "" {
		options.Logger.Warn("discord is enabled but no bot token is set; add one under Settings -> Connections -> Discord and restart")
		return discordWiring{}
	}
	client, err := discord.NewClient(token, options.Logger)
	if err != nil {
		options.Logger.Warn("discord client unavailable", "error", err)
		return discordWiring{}
	}
	wiring := buildDiscordWiring(cfg, options, accounts, client, sink, links)
	wiring.client = client
	return wiring
}

// buildDiscordWiring assembles handler and channel over any transport.
func buildDiscordWiring(cfg config.Config, options AppOptions, accounts web.AccountDirectory, transport discord.Transport, sink discord.EventSink, links *identityLinkCoordinator) discordWiring {
	handler := discord.NewHandler(discordUsers(accounts), sink, transport, options.Now, options.Logger)
	if links != nil {
		handler.WithLinkConsumer(links.consumeDiscord)
	}
	channel := discord.NewChannel(transport, discordRecipients(accounts), cfg.Server.PublicBaseURL, handler)
	return discordWiring{handler: handler, channel: channel}
}

// discordUsers maps a verified Discord user to its account against the
// live directory, so an unlink takes effect on the next message.
func discordUsers(accounts web.AccountDirectory) discord.UserResolver {
	return func(userID string) (string, bool) {
		if userID == "" {
			return "", false
		}
		for _, account := range accounts.Accounts() {
			if account.DiscordUserID == userID {
				return account.ID, true
			}
		}
		return "", false
	}
}

// discordRecipients maps an account to its linked Discord user, live, so
// delivery into a DM stops the moment the link is removed.
func discordRecipients(accounts web.AccountDirectory) discord.RecipientResolver {
	return func(accountID string) (string, bool) {
		account, ok := accounts.Account(accountID)
		if !ok || account.DiscordUserID == "" {
			return "", false
		}
		return account.DiscordUserID, true
	}
}

// open connects the gateway and routes messages into intake. Intake runs on
// the gateway's goroutine; a refused message costs nothing and an accepted
// one only enqueues.
func (w discordWiring) open(ctx context.Context, logger *slog.Logger) error {
	if w.client == nil {
		return nil
	}
	return w.client.Open(ctx, w.intake(logger))
}

func (w discordWiring) intake(logger *slog.Logger) func(context.Context, discord.Inbound) {
	return func(ctx context.Context, in discord.Inbound) {
		if err := w.handler.Handle(ctx, in); err != nil {
			logger.Error("discord intake failed", "message_id", in.MessageID, "error", err)
		}
	}
}

func (w discordWiring) close(logger *slog.Logger) {
	if w.client == nil {
		return
	}
	if err := w.client.Close(); err != nil {
		logger.Warn("discord gateway close failed", "error", err)
	}
}

func (w discordWiring) running() bool { return w.client != nil }
