package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
