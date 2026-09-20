package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/internal/web"
	"github.com/nigelteosw/eggy/plugins/auth/session"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// loginConfig is the environment half of the web login: which account the
// operator's credentials sign in, the username standing for it, and the
// password prehashed into the stored-password format. Hashing happens once
// here, at boot, so the login route never derives a key from a plaintext it
// holds in memory, and a wrong guess against this account costs the same
// verification a wrong local password does.
func loginConfig(cfg config.Config, secrets config.Secrets) (accountID, alias, hash string, err error) {
	accountID = cfg.PasswordAccountID()
	if strings.TrimSpace(secrets.UIPassword) == "" {
		return accountID, "", "", nil
	}
	hash, err = session.HashEnvironmentPassword(secrets.UIPassword)
	if err != nil {
		return "", "", "", fmt.Errorf("EGGY_UI_PASSWORD: %w", err)
	}
	return accountID, strings.TrimSpace(secrets.UIUserEmail), hash, nil
}

// reconcileAccountAuth aligns the credential rows with YAML membership at
// boot: every configured account gets a row if it has none, so it can be
// signed in or reached by /web, and every row whose account is no longer
// configured is retired, which is the cleanup a failed removal left for
// the next start. A retired row for a configured ID is left alone: that ID
// was removed once and pasting it back into YAML does not revive it.
func reconcileAccountAuth(ctx context.Context, database *sqlitestore.Store, cfg config.Config) error {
	configured := map[string]bool{}
	for _, account := range cfg.Principals() {
		configured[strings.ToLower(account.ID)] = true
		err := database.RegisterAccountAuth(ctx, account.ID)
		if err != nil && !errors.Is(err, ports.ErrAccountIDUsed) {
			return fmt.Errorf("register account %q: %w", account.ID, err)
		}
	}
	records, err := database.AccountAuthRecords(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Retired || configured[strings.ToLower(record.AccountID)] {
			continue
		}
		if err := database.RetireAccountAuth(ctx, record.AccountID); err != nil {
			return fmt.Errorf("retire removed account %q: %w", record.AccountID, err)
		}
	}
	return nil
}

// RecoveryWeb builds the safe-mode login surface for a config that failed to
// load: the same username/password login as the running panel, against the
// accounts the broken document still declares and the credentials and
// sessions in the existing database -- or, when even that cannot be
// established, a surface that says so and lets nobody in. The returned
// closer releases the database.
func RecoveryWeb(layout home.Layout, configPath string, getenv func(string) string, envSecrets config.Secrets, logger *slog.Logger) (web.WebUIConfig, func(), error) {
	identity, err := config.LoadRecoveryIdentity(configPath, getenv)
	if err != nil {
		logger.Warn("safe mode cannot identify anyone; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{}, func() {}, nil
	}
	database, err := sqlitestore.Open(home.At(identity.Config.DataDir).Database())
	if err != nil {
		logger.Warn("safe mode cannot open the session database; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{}, func() {}, nil
	}
	if err := reconcileAccountAuth(context.Background(), database, identity.Config); err != nil {
		_ = database.Close()
		logger.Warn("safe mode cannot reconcile account credentials; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{}, func() {}, nil
	}
	accountID, alias, hash, err := loginConfig(identity.Config, identity.Secrets)
	if err != nil {
		_ = database.Close()
		logger.Warn("safe mode cannot use the environment login; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{}, func() {}, nil
	}
	return web.WebUIConfig{
		Auth: database, Sessions: database, Accounts: newAccountDirectory("", getenv, identity.Config),
		PasswordAccountID: accountID, EnvironmentAlias: alias, EnvironmentPasswordHash: hash,
		PublicBaseURL: identity.Config.Server.PublicBaseURL,
	}, func() { _ = database.Close() }, nil
}

// webLoginLinkMinter is the /web command's link factory. It runs only for a
// context bootstrap marked with a verified Telegram sender, and mints only
// when the config as it is now still maps that exact sender to the acting
// account with Telegram on: an input queued before a sender was reassigned
// finds the mapping gone and mints nothing, as neither the old owner nor
// the newly linked person. The token is generated before the lock and
// stored inside it, against the account's current credential generation.
func (a *App) webLoginLinkMinter(database *sqlitestore.Store, configPath string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, senderID string) (string, error) {
		principal, err := ports.PrincipalFromContext(ctx)
		if err != nil {
			return "", err
		}
		raw, hash, err := session.NewToken()
		if err != nil {
			return "", err
		}
		now := a.now()
		mint := func(cfg config.Config, account config.AccountConfig) error {
			if !cfg.TelegramEnabled() || account.TelegramUserID == 0 || config.TelegramIDText(account.TelegramUserID) != senderID {
				return errors.New("this chat is not mapped to your account any more")
			}
			record, err := database.AccountAuth(ctx, account.ID)
			if err != nil || record.Retired {
				return errors.New("your account cannot sign in to the web panel")
			}
			return database.CreateWebLoginLink(ctx, hash, ports.WebLoginLink{
				AccountID: account.ID, SenderID: senderID, Generation: record.Generation,
				ExpiresAt: now.Add(webLoginLinkTTL),
			}, now)
		}
		if configPath == "" {
			account, ok := a.config.Account(principal.AccountID)
			if !ok {
				return "", errors.New("account is not configured")
			}
			if err := mint(a.config, account); err != nil {
				return "", err
			}
		} else if err := config.WithAccount(configPath, principal.AccountID, mint); err != nil {
			return "", err
		}
		return strings.TrimRight(a.config.Server.PublicBaseURL, "/") + "/auth/link#token=" + raw, nil
	}
}

// webLoginLinkTTL bounds how long a /web link is worth stealing: minutes
// rather than hours, because it travels through a chat transcript.
const webLoginLinkTTL = 5 * time.Minute
