package bootstrap

import (
	"log/slog"

	"github.com/nigelteosw/eggy/internal/auth/connections"
	"github.com/nigelteosw/eggy/internal/auth/grants"
	"github.com/nigelteosw/eggy/internal/config"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

// connectionIDs lists every chat connection whose credentials may be
// stored, for redaction: a stored bot token must be kept out of logs and
// traces exactly as an environment one is.
var connectionIDs = []string{config.DiscordConnection}

// openConnectionCredentials builds the sealed per-connection credential
// store, or nothing when there is no encryption key to seal under. A
// deployment without EGGY_ENCRYPTION_KEY still runs; its bots take their
// tokens from the environment, and the panel says so.
func openConnectionCredentials(database *sqlitestore.Store, secrets config.Secrets, logger *slog.Logger) *connections.Store {
	if secrets.EncryptionKey == "" {
		return nil
	}
	sealer, err := grants.NewSealer("connections", secrets.EncryptionKey)
	if err != nil {
		logger.Warn("connection credentials unavailable; bot tokens must come from the environment", "error", err)
		return nil
	}
	return connections.New(database.Auth(), sealer)
}

// discordBotToken resolves the token the bot runs under: the environment
// override first, so an operator who manages secrets there is never
// silently superseded by a stale stored one, then the panel-set credential.
func discordBotToken(secrets config.Secrets, credentials *connections.Store) string {
	if secrets.DiscordBotToken != "" {
		return secrets.DiscordBotToken
	}
	stored, err := credentials.Read(config.DiscordConnection)
	if err != nil {
		return ""
	}
	return stored["bot_token"]
}
