package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/web"
	googlelogin "github.com/nigelteosw/eggy/plugins/auth/google"
	"github.com/nigelteosw/eggy/plugins/auth/grants"
	sqlitestore "github.com/nigelteosw/eggy/plugins/store/sqlite"
)

// googleLoginCallbackPath is the one callback registered with the Web
// application client. It is derived from the validated public base URL and
// never configurable on its own: a redirect an operator could point
// elsewhere is a redirect an attacker could too.
const googleLoginCallbackPath = "/auth/google/callback"

// newGoogleLogin builds the inbound Sign-In client, or nothing outside
// account mode: a legacy deployment mounts no login routes and constructs no
// provider client. The verifier sealer is the same AES-GCM sealer every grant
// uses, under the same key, labelled so an error names the login.
func newGoogleLogin(cfg config.Config, secrets config.Secrets, options AppOptions) (web.GoogleLogin, web.VerifierSealer, error) {
	if !cfg.AccountMode() {
		return nil, nil, nil
	}
	sealer, err := grants.NewSealer("login", secrets.EncryptionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("open login sealer: %w", err)
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	callback := strings.TrimRight(cfg.Server.PublicBaseURL, "/") + googleLoginCallbackPath
	login, err := googlelogin.New(cfg.Web.GoogleLogin.ClientID, secrets.GoogleLoginClientSecret, callback, client)
	if err != nil {
		return nil, nil, fmt.Errorf("configure Google Sign-In: %w", err)
	}
	return login, sealer, nil
}

// RecoveryWeb builds the safe-mode login surface for a config that failed to
// load. Legacy deployments keep the password login the supervisor already
// configured. An account-mode deployment gets Google Sign-In against the
// accounts the broken document still declares and the sessions in the
// existing database -- or, when even that cannot be established, a surface
// that says so and lets nobody in. The returned closer releases the database.
func RecoveryWeb(layout home.Layout, configPath string, getenv func(string) string, envSecrets config.Secrets, logger *slog.Logger) (web.WebUIConfig, func(), error) {
	legacy := web.WebUIConfig{
		UserEmail: envSecrets.UIUserEmail, Password: envSecrets.UIPassword,
		SigningKey: []byte(envSecrets.EncryptionKey),
	}
	identity, err := config.LoadRecoveryIdentity(configPath, getenv)
	if !identity.AccountMode {
		return legacy, func() {}, nil
	}
	if err != nil {
		logger.Warn("safe mode cannot identify anyone; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{AccountMode: true}, func() {}, nil
	}
	database, err := sqlitestore.Open(home.At(identity.Config.DataDir).Database())
	if err != nil {
		logger.Warn("safe mode cannot open the session database; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{AccountMode: true}, func() {}, nil
	}
	login, sealer, err := newGoogleLogin(identity.Config, identity.Secrets, AppOptions{})
	if err != nil {
		_ = database.Close()
		logger.Warn("safe mode cannot configure Google Sign-In; repair config.yaml on the host", "error", err)
		return web.WebUIConfig{AccountMode: true}, func() {}, nil
	}
	return web.WebUIConfig{
		AccountMode: true, Sessions: database, Accounts: accountDirectory{config: identity.Config},
		GoogleLogin: login, Identities: database, LoginSealer: sealer,
		PublicBaseURL: identity.Config.Server.PublicBaseURL,
	}, func() { _ = database.Close() }, nil
}
