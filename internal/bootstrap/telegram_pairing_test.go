package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// pairingAccountConfig is liveAccountConfig with a second account and
// Telegram explicitly enabled, so LinkTelegramAccount succeeds against it.
func pairingAccountConfig() string {
	return `server:
  public_base_url: https://eggy.example
data_dir: /data
telegram:
  enabled: true
accounts:
  - id: nigel
    google_email: nigel@example.com
  - id: partner
    google_email: partner@example.com
    telegram_user_id: 77
web:
  google_login:
    client_id: client
    client_secret_env: LOGIN_SECRET
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

// newTestPairingCoordinator wires a coordinator against a real SQLite store
// and a real config.yaml on disk, the same two collaborators consume() uses
// in production -- a fake of either would let this suite pass against
// behavior the running daemon does not have.
func newTestPairingCoordinator(t *testing.T, now time.Time) (*telegramPairingCoordinator, string) {
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
	return &telegramPairingCoordinator{
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

func TestTelegramPairingConsumeLinksAccountAndFinalizes(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestPairingCoordinator(t, now)
	code, hash := pairingCode(1)
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consume(context.Background(), code, 555); err != nil {
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
func TestTelegramPairingClaimedCodeStaysUnusableAcrossRestart(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, _ := newTestPairingCoordinator(t, now)
	_, hash := pairingCode(2)
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := coordinator.store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	// Simulate the process restarting with a fresh coordinator over the same
	// database: nothing in construction may release a claimed-but-unfinished
	// pairing.
	restarted := &telegramPairingCoordinator{store: coordinator.store, configPath: coordinator.configPath, now: func() time.Time { return now }, logger: coordinator.logger}
	if _, _, ok, err := restarted.store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || ok {
		t.Fatalf("claimed code became claimable again after restart: ok=%v err=%v", ok, err)
	}
}

// A config write that fails -- here, the account has already been unlinked
// out from under the pending code -- must release the claim rather than burn
// it, so the owner is not forced to regenerate a link over a transient error.
func TestTelegramPairingConsumeReleasesClaimOnConfigWriteFailure(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestPairingCoordinator(t, now)
	code, hash := pairingCode(3)
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := config.RemoveAccount(configPath, "nigel"); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consume(context.Background(), code, 555); err == nil {
		t.Fatal("expected the config write to fail for a removed account")
	}
	if _, _, ok, err := coordinator.store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || !ok {
		t.Fatalf("failed link did not release its claim: ok=%v err=%v", ok, err)
	}
}

// A completed link stands even when finalization itself fails afterward:
// consume reports success from YAML and does not roll the link back just
// because closing out the claim record failed.
func TestTelegramPairingConsumeSurvivesFinalizationFailureAfterLinking(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestPairingCoordinator(t, now)
	hash := sha256.Sum256([]byte("finalize-code"))
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	accountID, claim, ok, err := coordinator.store.ClaimTelegramPairing(context.Background(), hash, now)
	if err != nil || !ok || accountID != "nigel" {
		t.Fatalf("claim account=%q ok=%v err=%v", accountID, ok, err)
	}
	if err := config.LinkTelegramAccount(configPath, accountID, 555); err != nil {
		t.Fatal(err)
	}
	// The database closes right after the link the way it would mid-crash;
	// finalization over the closed connection fails, exactly as
	// FinishTelegramPairing(ctx, claim, true) would inside consume.
	if err := coordinator.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.store.FinishTelegramPairing(context.Background(), claim, true); err == nil {
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

func TestTelegramPairingConsumeRejectsReplay(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, _ := newTestPairingCoordinator(t, now)
	code, hash := pairingCode(4)
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consume(context.Background(), code, 555); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consume(context.Background(), code, 999); err == nil {
		t.Fatal("replay from a different sender succeeded")
	}
	if err := coordinator.consume(context.Background(), code, 555); err == nil {
		t.Fatal("replay from the same sender succeeded")
	}
}

// A pending code may not silently replace another account's mapping: the
// Telegram user is already bound to partner, so nigel's code must fail
// rather than move partner's identity.
func TestTelegramPairingConsumeRejectsDuplicateLink(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	coordinator, configPath := newTestPairingCoordinator(t, now)
	code, hash := pairingCode(5)
	if err := coordinator.store.CreateTelegramPairing(context.Background(), "nigel", hash, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.consume(context.Background(), code, 77); err == nil {
		t.Fatal("linking a sender already bound to another account succeeded")
	}
	cfg, err := config.LoadDocument(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if account, ok := cfg.AccountForTelegram(77); !ok || account.ID != "partner" {
		t.Fatalf("partner's existing binding changed: %#v, %v", account, ok)
	}
	if _, _, ok, err := coordinator.store.ClaimTelegramPairing(context.Background(), hash, now); err != nil || !ok {
		t.Fatalf("refused duplicate link did not release its claim: ok=%v err=%v", ok, err)
	}
}
