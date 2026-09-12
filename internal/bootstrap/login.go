package bootstrap

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/web"
	googlelogin "github.com/nigelteosw/eggy/plugins/auth/google"
	"github.com/nigelteosw/eggy/plugins/auth/grants"
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
