package bootstrap

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/telegram"
)

// telegramWiring is everything the Telegram surface contributes to an App:
// the client and channel, the selector's tool, the webhook route, and the
// bot's command suggestions, resolved once instead of at four points in
// NewApp.
//
// Telegram is optional, and its absence is this struct's zero value — a
// web-only deployment gets no client, no channel, no selector tool, and no
// webhook route. Every method below is safe to call on that zero value.
type telegramWiring struct {
	client   *telegram.Client
	selector *telegram.Selector
	// Separate interface fields, assigned only when a client exists, so they
	// stay *true* nils. Assigning client unconditionally would box a nil
	// *telegram.Client into a non-nil interface, which compares non-nil while
	// wrapping a nil pointer and silently defeats newRoutedChannel's and
	// NewWebhookHandler's nil checks.
	channel      ports.Channel
	acknowledger telegram.CallbackAcknowledger
}

func newTelegramWiring(cfg config.Config, secrets config.Secrets, options AppOptions) telegramWiring {
	if options.FakeAdapters || !cfg.TelegramEnabled() {
		return telegramWiring{}
	}
	client := telegram.NewClient(options.TelegramBaseURL, secrets.TelegramBotToken, telegramChats(cfg), options.HTTPClient)
	return telegramWiring{
		client:       client,
		selector:     telegram.NewSelector(client, options.Now, 10*time.Minute),
		channel:      client,
		acknowledger: client,
	}
}

// telegramSenders maps a verified numeric sender to its account, from the
// configured list in either shape. Unmapped senders resolve to nothing.
func telegramSenders(cfg config.Config) telegram.SenderResolver {
	return func(sender int64) (string, bool) {
		account, ok := cfg.AccountForTelegram(sender)
		if !ok {
			return "", false
		}
		return account.ID, true
	}
}

// telegramChats maps an account to its private chat: the sender ID, since a
// private chat with a bot is numbered by the person. An account without a
// Telegram sender has no chat, and the client reports that rather than
// borrowing someone else's.
func telegramChats(cfg config.Config) telegram.ChatResolver {
	return func(accountID string) (string, bool) {
		account, ok := cfg.Account(accountID)
		if !ok || account.TelegramUserID == 0 {
			return "", false
		}
		return strconv.FormatInt(account.TelegramUserID, 10), true
	}
}

// tools is the selector's tool, or nothing when Telegram is absent: an
// unconfigured capability costs no schema.
func (w telegramWiring) tools() []ports.Tool {
	if w.selector == nil {
		return nil
	}
	return []ports.Tool{w.selector.Tool()}
}

// webhook is the update route, or nil when Telegram is not configured;
// web.NewHTTPHandler serves the path as unavailable for a nil handler.
//
// Gated on configuration alone, deliberately not on FakeAdapters: an
// integration test configures Telegram and posts updates here while every
// outbound call is faked, so the route must exist even when the client does
// not.
func (w telegramWiring) webhook(cfg config.Config, secrets config.Secrets, sink telegram.EventSink) http.Handler {
	if !cfg.TelegramEnabled() {
		return nil
	}
	handler := telegram.NewWebhookHandler(telegramSenders(cfg), secrets.TelegramWebhookSecret, sink, w.acknowledger)
	if w.client != nil {
		handler.WithImageDownloader(w.client)
	}
	if w.selector != nil {
		handler.WithSelectionResolver(w.selector.Resolve)
	}
	return handler
}

// registerCommands publishes the slash-command suggestions Telegram shows in
// its composer. A failure is logged, not fatal: refusing to boot over a
// cosmetic API call would turn a Telegram outage into an Eggy outage.
func (w telegramWiring) registerCommands(ctx context.Context, logger *slog.Logger) {
	if w.client == nil {
		return
	}
	autocomplete := commands.TelegramAutocomplete()
	suggestions := make([]telegram.BotCommand, 0, len(autocomplete))
	for _, command := range autocomplete {
		suggestions = append(suggestions, telegram.BotCommand{Name: command.Name, Description: command.Description})
	}
	if err := w.client.SetCommands(ctx, suggestions); err != nil {
		logger.Warn("failed to register Telegram command suggestions", "error", err)
	}
}
