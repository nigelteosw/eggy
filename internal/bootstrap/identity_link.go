package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	sqlitestore "github.com/nigelteosw/eggy/internal/storage/sqlite"
)

// TelegramConnection is the identity-link connection ID Telegram pairing
// tokens are created and redeemed under.
const TelegramConnection = "telegram"

// identityLinkCoordinator serialises token creation and redemption for every
// chat connection over one store. Each connection contributes only the
// config write that binds the verified subject to the claimed account; the
// claim, release and finalise semantics are shared and never span a config
// lock with a SQLite transaction.
type identityLinkCoordinator struct {
	mu         sync.Mutex
	store      *sqlitestore.Store
	configPath string
	now        func() time.Time
	logger     *slog.Logger
}

func (c *identityLinkCoordinator) CreateIdentityLink(ctx context.Context, account, connection string, hash [32]byte, expires time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.CreateIdentityLink(ctx, account, connection, hash, expires)
}
func (c *identityLinkCoordinator) ClaimIdentityLink(ctx context.Context, connection string, hash [32]byte, now time.Time) (string, [16]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.ClaimIdentityLink(ctx, connection, hash, now)
}
func (c *identityLinkCoordinator) FinishIdentityLink(ctx context.Context, claim [16]byte, success bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.FinishIdentityLink(ctx, claim, success)
}
func (c *identityLinkCoordinator) DeleteIdentityLinks(ctx context.Context, account, connection string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.DeleteIdentityLinks(ctx, account, connection)
}

var errInvalidLink = errors.New("linking token is invalid or expired")

// consume redeems one token on one connection: claim it, bind the subject
// through link, then finalise -- or release the claim when the binding is
// refused so the owner can retry without a new token. A link that succeeded
// stands even when finalisation fails afterward; YAML is the authority.
func (c *identityLinkCoordinator) consume(ctx context.Context, connection, payload string, link func(accountID string) error) error {
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(decoded) != 32 {
		return errInvalidLink
	}
	hash := sha256.Sum256(decoded)
	c.mu.Lock()
	defer c.mu.Unlock()
	accountID, claimID, ok, err := c.store.ClaimIdentityLink(ctx, connection, hash, c.now())
	if err != nil {
		return err
	}
	if !ok {
		return errInvalidLink
	}
	if err := link(accountID); err != nil {
		if releaseErr := c.store.FinishIdentityLink(ctx, claimID, false); releaseErr != nil {
			c.logger.Error("failed to release identity link claim", "connection", connection, "account", accountID, "error", releaseErr)
		}
		return err
	}
	if err := c.store.FinishIdentityLink(ctx, claimID, true); err != nil {
		c.logger.Error("identity linked but finalization failed", "connection", connection, "account", accountID, "error", err)
	}
	return nil
}

// consumeTelegram is the Telegram webhook's /start <token> redemption.
func (c *identityLinkCoordinator) consumeTelegram(ctx context.Context, payload string, sender int64) error {
	return c.consume(ctx, TelegramConnection, payload, func(accountID string) error {
		return config.LinkTelegramAccount(c.configPath, accountID, sender)
	})
}

// consumeDiscord is the Discord DM's /link <token> redemption.
func (c *identityLinkCoordinator) consumeDiscord(ctx context.Context, payload, userID string) error {
	if _, err := strconv.ParseUint(userID, 10, 64); err != nil {
		return errInvalidLink
	}
	return c.consume(ctx, config.DiscordConnection, payload, func(accountID string) error {
		return config.LinkDiscordAccount(c.configPath, accountID, userID)
	})
}
