package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DiscordConfig is the optional personal Discord channel: one bot, talking
// to linked owners in private DMs. The bot token is never here: it is set
// from the panel and sealed in the connection credential store, or
// overridden by DISCORD_BOT_TOKEN.
type DiscordConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`
	// ApplicationID is the bot's application ID, used only to build the
	// "open a DM with the bot" link the panel shows. It is public and
	// optional; linking works without it by pasting the token into a DM.
	ApplicationID string `yaml:"application_id,omitempty"`
}

// DiscordConnection is the identity-link connection ID Discord tokens are
// created and redeemed under.
const DiscordConnection = "discord"

// discordSnowflake bounds a Discord user or application ID: an opaque
// decimal snowflake, never a username, which is reassignable.
var discordSnowflake = regexp.MustCompile(`^[0-9]{1,32}$`)

// DiscordEnabled reports whether the Discord bot should run.
func (c Config) DiscordEnabled() bool { return c.Discord.Enabled }

// AccountForDiscord resolves the account a verified Discord user speaks
// for. Unmapped users resolve to nothing, and nothing is the answer: there
// is no first-configured-user fallback, and no numeric coincidence with a
// Telegram ID counts.
func (c Config) AccountForDiscord(userID string) (AccountConfig, bool) {
	if userID == "" {
		return AccountConfig{}, false
	}
	for _, account := range c.Principals() {
		if account.DiscordUserID == userID {
			return account, true
		}
	}
	return AccountConfig{}, false
}

func (c Config) validateDiscord() error {
	if c.Discord.ApplicationID != "" && !discordSnowflake.MatchString(c.Discord.ApplicationID) {
		return fmt.Errorf("discord.application_id %q is not a Discord application ID", c.Discord.ApplicationID)
	}
	seen := map[string]bool{}
	for _, account := range c.Accounts {
		if account.DiscordUserID == "" {
			continue
		}
		if !discordSnowflake.MatchString(account.DiscordUserID) {
			return fmt.Errorf("account %q discord_user_id %q is not a Discord user ID", account.ID, account.DiscordUserID)
		}
		if seen[account.DiscordUserID] {
			return fmt.Errorf("duplicate account discord_user_id %s", account.DiscordUserID)
		}
		seen[account.DiscordUserID] = true
	}
	return nil
}

// SetDiscord saves the Discord section from the panel's form.
func SetDiscord(path string, enabled bool, applicationID string) error {
	return mutate(path, func(cfg *Config) error {
		cfg.Discord = DiscordConfig{Enabled: enabled, ApplicationID: strings.TrimSpace(applicationID)}
		return nil
	})
}

// LinkDiscordAccount binds a verified Discord user to an account, refusing
// a user already bound elsewhere and an account already bound to someone
// else: unlinking is explicit, never a side effect of a new token.
func LinkDiscordAccount(path, accountID, discordUserID string) error {
	if !discordSnowflake.MatchString(discordUserID) {
		return errors.New("discord user id is not a Discord user ID")
	}
	return mutate(path, func(cfg *Config) error {
		if !cfg.DiscordEnabled() {
			return errors.New("Discord is not enabled")
		}
		index := -1
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ID == accountID {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("account %q is not configured", accountID)
		}
		if existing, ok := cfg.AccountForDiscord(discordUserID); ok && existing.ID != accountID {
			return fmt.Errorf("discord user %s is already linked to another account", discordUserID)
		}
		if cfg.Accounts[index].DiscordUserID != "" && cfg.Accounts[index].DiscordUserID != discordUserID {
			return fmt.Errorf("account %q is already linked; unlink it first", accountID)
		}
		cfg.Accounts[index].DiscordUserID = discordUserID
		return nil
	})
}

func UnlinkDiscordAccount(path, accountID string) error {
	return mutate(path, func(cfg *Config) error {
		for i := range cfg.Accounts {
			if cfg.Accounts[i].ID == accountID {
				if cfg.Accounts[i].DiscordUserID == "" {
					return errNoConfigChange
				}
				cfg.Accounts[i].DiscordUserID = ""
				return nil
			}
		}
		return fmt.Errorf("account %q is not configured", accountID)
	})
}

// DiscordBotTokenEnv names the optional environment override for the bot
// token. The token is ordinarily set from the panel and kept sealed in the
// connection credential store; the variable exists for operators who manage
// every secret in the deployment environment. It is never required at boot:
// an enabled Discord section without a token runs no bot and says so.
const DiscordBotTokenEnv = "DISCORD_BOT_TOKEN"
