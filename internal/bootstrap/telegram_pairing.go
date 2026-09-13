package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

type telegramPairingCoordinator struct {
	mu         sync.Mutex
	store      *sqlitestore.Store
	configPath string
	now        func() time.Time
	logger     *slog.Logger
}

func (c *telegramPairingCoordinator) CreateTelegramPairing(ctx context.Context, account string, hash [32]byte, expires time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.CreateTelegramPairing(ctx, account, hash, expires)
}
func (c *telegramPairingCoordinator) ClaimTelegramPairing(ctx context.Context, hash [32]byte, now time.Time) (string, [16]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.ClaimTelegramPairing(ctx, hash, now)
}
func (c *telegramPairingCoordinator) FinishTelegramPairing(ctx context.Context, claim [16]byte, success bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.FinishTelegramPairing(ctx, claim, success)
}
func (c *telegramPairingCoordinator) DeleteTelegramPairings(ctx context.Context, account string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.DeleteTelegramPairings(ctx, account)
}

func (c *telegramPairingCoordinator) consume(ctx context.Context, payload string, sender int64) error {
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(decoded) != 32 {
		return errors.New("invalid Telegram pairing")
	}
	hash := sha256.Sum256(decoded)
	c.mu.Lock()
	defer c.mu.Unlock()
	accountID, claimID, ok, err := c.store.ClaimTelegramPairing(ctx, hash, c.now())
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("invalid Telegram pairing")
	}
	if err := config.LinkTelegramAccount(c.configPath, accountID, sender); err != nil {
		if releaseErr := c.store.FinishTelegramPairing(ctx, claimID, false); releaseErr != nil {
			c.logger.Error("failed to release Telegram pairing claim", "account", accountID, "error", releaseErr)
		}
		return err
	}
	if err := c.store.FinishTelegramPairing(ctx, claimID, true); err != nil {
		c.logger.Error("Telegram linked but pairing finalization failed", "account", accountID, "error", err)
	}
	return nil
}
