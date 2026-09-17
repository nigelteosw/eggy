package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discordTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := strings.Replace(accountConfig(), "agent:", "discord:\n  enabled: true\nagent:", 1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDocument(path); err != nil {
		t.Fatalf("fixture does not load: %v", err)
	}
	return path
}

func TestLinkDiscordAccountRefusesDuplicateBindings(t *testing.T) {
	path := discordTestConfig(t)
	if err := LinkDiscordAccount(path, "nigel", "1001"); err != nil {
		t.Fatal(err)
	}
	if err := LinkDiscordAccount(path, "partner", "1001"); err == nil {
		t.Fatal("one Discord user bound to two accounts")
	}
	if err := LinkDiscordAccount(path, "nigel", "1002"); err == nil {
		t.Fatal("a linked account rebound without unlinking")
	}
	// Relinking the same user is idempotent.
	if err := LinkDiscordAccount(path, "nigel", "1001"); err != nil {
		t.Fatal(err)
	}
	if err := LinkDiscordAccount(path, "nigel", "not-a-snowflake"); err == nil {
		t.Fatal("a username-looking id was accepted")
	}
	if err := LinkDiscordAccount(path, "missing", "1003"); err == nil {
		t.Fatal("linked a removed account")
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.AccountForDiscord("1001"); !ok || account.ID != "nigel" {
		t.Fatalf("AccountForDiscord=%+v ok=%v", account, ok)
	}
	if _, ok := cfg.AccountForDiscord(""); ok {
		t.Fatal("empty user matched")
	}
	if err := UnlinkDiscordAccount(path, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkDiscordAccount(path, "nigel"); err != nil {
		t.Fatalf("unlinking an unlinked account must be a no-op: %v", err)
	}
	cfg, _ = LoadDocument(path)
	if _, ok := cfg.AccountForDiscord("1001"); ok {
		t.Fatal("unlink did not take")
	}
}

func TestLinkDiscordAccountRefusesWhenDisabled(t *testing.T) {
	path := discordTestConfig(t)
	if err := SetDiscord(path, false, ""); err != nil {
		t.Fatal(err)
	}
	if err := LinkDiscordAccount(path, "nigel", "1001"); err == nil {
		t.Fatal("linked while Discord is disabled")
	}
	if err := SetDiscord(path, true, "abc"); err == nil {
		t.Fatal("a non-numeric application id was accepted")
	}
	if err := SetDiscord(path, true, "  4242 "); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadDocument(path)
	if !cfg.DiscordEnabled() || cfg.Discord.ApplicationID != "4242" {
		t.Fatalf("discord=%+v", cfg.Discord)
	}
}

func TestDiscordBotTokenFromTheEnvironmentIsRedactedButNeverRequired(t *testing.T) {
	secrets := SecretsFromEnv(func(key string) string {
		if key == DiscordBotTokenEnv {
			return "bot-token"
		}
		return ""
	})
	found := false
	for _, value := range secrets.Values() {
		found = found || value == "bot-token"
	}
	if !found {
		t.Fatal("the bot token is not redacted")
	}
	var cfg Config
	cfg.Discord.Enabled = true
	if err := cfg.validateSecrets(Secrets{}); err != nil && strings.Contains(err.Error(), DiscordBotTokenEnv) {
		t.Fatalf("the token is set at runtime from the panel and must not gate boot: %v", err)
	}
}

func TestValidateRefusesDuplicateDiscordUserIDs(t *testing.T) {
	path := discordTestConfig(t)
	cfg, _ := LoadDocument(path)
	cfg.Accounts[0].DiscordUserID = "1"
	cfg.Accounts[1].DiscordUserID = "1"
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate discord_user_id accepted")
	}
	cfg.Accounts[1].DiscordUserID = "someone#1234"
	if err := cfg.Validate(); err == nil {
		t.Fatal("username accepted as discord_user_id")
	}
}
