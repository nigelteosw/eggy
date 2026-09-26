package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

// pairingAccountConfig is liveAccountConfig with a second account and
// Telegram explicitly enabled, so LinkTelegramAccount succeeds against it.
func pairingAccountConfig() string {
	return `server:
  public_base_url: https://eggy.example
data_dir: /data
telegram:
  enabled: true
discord:
  enabled: true
accounts:
  - id: nigel
  - id: partner
    telegram_user_id: 77
    discord_user_id: "77"
web:
  password_account_id: nigel
agent:
  default_model: model
providers:
  provider:
    adapter: openai_compatible
    base_url: https://api.example.com
    api_key_env: MODEL_KEY
models:
  model:
    provider: provider
    model: model-id
repositories: []
runner:
  root: /data/runs
  timeout: 5m
  retention: 15m
  max_output_bytes: 1048576
  allowed_env: [PATH]
`
}

// newTestLinkCoordinator wires a coordinator against a real SQLite store
// and a real config.yaml on disk, the same two collaborators consume() uses
// in production -- a fake of either would let this suite pass against
// behavior the running daemon does not have.
func newTestLinkCoordinator(t *testing.T, now time.Time) (*identityLinkCoordinator, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(pairingAccountConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(filepath.Join(dir, "eggy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &identityLinkCoordinator{
		store: store, configPath: configPath,
		now:    func() time.Time { return now },
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}, configPath
}

// pairingCode builds a code the same shape consume() expects: 32 raw bytes,
// base64url-encoded for the Telegram deep link, hashed with SHA-256 for
// storage. seed only varies the bytes between tests; it carries no meaning.
func pairingCode(seed byte) (string, [32]byte) {
	var raw [32]byte
	raw[0] = seed
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:])
}

func TestIdentityLinkTelegramConsumeLinksAccountAndFinalizes(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	code, hash := pairingCode(1)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 555); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.Account("nigel"); !ok || account.TelegramUserID != 555 {
		t.Fatalf("nigel = %#v, %v", account, ok)
	}
}

// A crash after the claim but before finalization must never make the code
// usable again on its own: only an explicit release does that, because YAML
// may already hold the link by the time a process restarts.
func TestIdentityLinkTelegramClaimedCodeStaysUnusableAcrossRestart(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, _ := newTestLinkCoordinator(t, now)
	_, hash := pairingCode(2)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := coordinator.store.ClaimIdentityLink(context.Background(), TelegramConnection, hash, now); err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	// Simulate the process restarting with a fresh coordinator over the same
	// database: nothing in construction may release a claimed-but-unfinished
	// pairing.
	restarted := &identityLinkCoordinator{store: coordinator.store, configPath: coordinator.configPath, now: func() time.Time { return now }, logger: coordinator.logger}
	if _, _, ok, err := restarted.store.ClaimIdentityLink(context.Background(), TelegramConnection, hash, now); err != nil || ok {
		t.Fatalf("claimed code became claimable again after restart: ok=%v err=%v", ok, err)
	}
}

// A config write that fails -- here, the account has already been unlinked
// out from under the pending code -- must release the claim rather than burn
// it, so the owner is not forced to regenerate a link over a transient error.
func TestIdentityLinkTelegramConsumeReleasesClaimOnConfigWriteFailure(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	code, hash := pairingCode(3)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// nigel is bound to the environment login; move the binding first so
	// the removal is refused for the right reason only.
	if err := config.ConvertToAccounts(configPath, config.ConvertInput{}); err == nil {
		t.Fatal("conversion of an accounts deployment must fail")
	}
	body, _ := os.ReadFile(configPath)
	if err := os.WriteFile(configPath, []byte(strings.Replace(string(body), "password_account_id: nigel", "password_account_id: partner", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.RemoveAccount(configPath, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 555); err == nil {
		t.Fatal("expected the config write to fail for a removed account")
	}
	if _, _, ok, err := coordinator.store.ClaimIdentityLink(context.Background(), TelegramConnection, hash, now); err != nil || !ok {
		t.Fatalf("failed link did not release its claim: ok=%v err=%v", ok, err)
	}
}

// A completed link stands even when finalization itself fails afterward:
// consume reports success from YAML and does not roll the link back just
// because closing out the claim record failed.
func TestIdentityLinkTelegramConsumeSurvivesFinalizationFailureAfterLinking(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	hash := sha256.Sum256([]byte("finalize-code"))
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	accountID, claim, ok, err := coordinator.store.ClaimIdentityLink(context.Background(), TelegramConnection, hash, now)
	if err != nil || !ok || accountID != "nigel" {
		t.Fatalf("claim account=%q ok=%v err=%v", accountID, ok, err)
	}
	if err := config.LinkTelegramAccount(configPath, accountID, 555); err != nil {
		t.Fatal(err)
	}
	// The database closes right after the link the way it would mid-crash;
	// finalization over the closed connection fails, exactly as
	// FinishIdentityLink(ctx, claim, true) would inside consume.
	if err := coordinator.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.store.FinishIdentityLink(context.Background(), claim, true); err == nil {
		t.Fatal("expected finalization over a closed store to fail")
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.AccountForTelegram(555); !ok || account.ID != "nigel" {
		t.Fatalf("link did not survive a failed finalization: %#v, %v", account, ok)
	}
}

func TestIdentityLinkTelegramConsumeRejectsReplay(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, _ := newTestLinkCoordinator(t, now)
	code, hash := pairingCode(4)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 555); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 999); err == nil {
		t.Fatal("replay from a different sender succeeded")
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 555); err == nil {
		t.Fatal("replay from the same sender succeeded")
	}
}

// A pending code may not silently replace another account's mapping: the
// Telegram user is already bound to partner, so nigel's code must fail
// rather than move partner's identity.
func TestIdentityLinkTelegramConsumeRejectsDuplicateLink(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	code, hash := pairingCode(5)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", TelegramConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeTelegram(context.Background(), code, 77); err == nil {
		t.Fatal("linking a sender already bound to another account succeeded")
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.AccountForTelegram(77); !ok || account.ID != "partner" {
		t.Fatalf("partner's existing binding changed: %#v, %v", account, ok)
	}
	if _, _, ok, err := coordinator.store.ClaimIdentityLink(context.Background(), TelegramConnection, hash, now); err != nil || !ok {
		t.Fatalf("refused duplicate link did not release its claim: ok=%v err=%v", ok, err)
	}
}

func TestIdentityLinkDiscordConsumeLinksOnlyItsOwnConnection(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	code, hash := pairingCode(6)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", config.DiscordConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A Discord token pasted into Telegram redeems nothing.
	if err := coordinator.consumeTelegram(context.Background(), code, 555); err == nil {
		t.Fatal("a Discord token redeemed a Telegram binding")
	}
	if err := coordinator.consumeDiscord(context.Background(), code, "not-a-user"); err == nil {
		t.Fatal("a non-numeric Discord user was accepted")
	}
	// Same numeric subject as partner's Telegram is no match: partner's
	// Discord binding is a separate field, and it is already 77.
	if err := coordinator.consumeDiscord(context.Background(), code, "77"); err == nil {
		t.Fatal("linking a Discord user already bound to another account succeeded")
	}
	if err := coordinator.consumeDiscord(context.Background(), code, "9001"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.AccountForDiscord("9001"); !ok || account.ID != "nigel" {
		t.Fatalf("nigel = %#v, %v", account, ok)
	}
	if account, ok := cfg.Account("nigel"); !ok || account.TelegramUserID != 0 {
		t.Fatalf("Discord linking touched Telegram: %#v", account)
	}
	if err := coordinator.consumeDiscord(context.Background(), code, "9001"); err == nil {
		t.Fatal("replay succeeded")
	}
}

func TestIdentityLinkDiscordConsumeRefusesWhenDisabled(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestLinkCoordinator(t, now)
	if err := config.SetDiscord(configPath, false, ""); err != nil {
		t.Fatal(err)
	}
	code, hash := pairingCode(7)
	if err := coordinator.store.CreateIdentityLink(context.Background(), "nigel", config.DiscordConnection, hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consumeDiscord(context.Background(), code, "9001"); err == nil {
		t.Fatal("linked while Discord is disabled")
	}
	if _, _, ok, err := coordinator.store.ClaimIdentityLink(context.Background(), config.DiscordConnection, hash, now); err != nil || !ok {
		t.Fatalf("refused link did not release its claim: ok=%v err=%v", ok, err)
	}
}
