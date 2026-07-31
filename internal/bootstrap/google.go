package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/ports"
	googleadapter "github.com/nigelteosw/eggy/plugins/tools/google"
)

// defaultGoogleScopes covers the products Eggy exposes, and nothing beyond
// them. They are requested together because one desktop client holds one
// grant: narrowing later means re-consenting, so the set is chosen once at
// login from the products actually configured.
var defaultGoogleScopes = map[string]string{
	"gmail":    "https://www.googleapis.com/auth/gmail.modify",
	"calendar": "https://www.googleapis.com/auth/calendar",
	"drive":    "https://www.googleapis.com/auth/drive.readonly",
	"docs":     "https://www.googleapis.com/auth/documents.readonly",
	"sheets":   "https://www.googleapis.com/auth/spreadsheets",
	"contacts": "https://www.googleapis.com/auth/contacts.readonly",
}

// newGoogleWorkspace returns nil when Google is not configured, which is what
// makes an absent capability cost nothing: no store is opened, no tool is
// built, and no scope is ever requested.
func newGoogleWorkspace(cfg config.Config, secrets config.Secrets, options AppOptions) (*googleadapter.Auth, *googleadapter.Workspace, error) {
	if !cfg.Google.Enabled {
		return nil, nil, nil
	}
	layout := home.At(cfg.DataDir)
	store, err := googleadapter.OpenTokenStore(layout.Auth(), secrets.EncryptionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("open Google token store: %w", err)
	}
	adapterConfig := googleadapter.Config{
		ClientID: cfg.Google.ClientID, ClientSecret: secrets.GoogleClientSecret,
		Scopes: googleScopes(cfg.Google), Timeout: cfg.Google.Timeout.Value(), MaxOutputBytes: cfg.Google.MaxOutputBytes,
	}
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	auth := googleadapter.NewAuth(adapterConfig, store, client, options.Now)
	return auth, googleadapter.NewWorkspace(auth, adapterConfig), nil
}

// googleScopes prefers what the owner wrote. Falling back to the scope each
// configured product needs keeps the common case free of a scope list nobody
// enjoys assembling, while leaving the narrow case reachable.
//
// Product names are matched exactly, against the canonical spelling
// config.applyDefaults guarantees. It must stay that way: when this matched
// exactly and the adapter matched case-insensitively, a config naming "Gmail"
// got the tool registered and no scope requested, which reads as a broken API
// rather than a misconfiguration.
func googleScopes(cfg config.GoogleConfig) []string {
	if len(cfg.Scopes) > 0 {
		return append([]string(nil), cfg.Scopes...)
	}
	scopes := make([]string, 0, len(cfg.Products))
	for _, product := range cfg.Products {
		if scope, known := defaultGoogleScopes[product]; known {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func googleTools(workspace *googleadapter.Workspace, cfg config.GoogleConfig, now func() time.Time) []ports.Tool {
	if workspace == nil {
		return nil
	}
	return googleadapter.Tools(workspace, cfg.Products, now)
}

// googleAdmin adapts the adapter to the command surface, so internal/commands
// stays free of the plugin package exactly as it does for MCP.
type googleAdmin struct{ auth *googleadapter.Auth }

func newGoogleAdmin(auth *googleadapter.Auth) *googleAdmin {
	if auth == nil {
		return nil
	}
	return &googleAdmin{auth: auth}
}

func (a *googleAdmin) BeginLogin(ctx context.Context) (string, error) { return a.auth.BeginLogin(ctx) }

func (a *googleAdmin) CompleteLogin(ctx context.Context, code, state string) error {
	return a.auth.CompleteLogin(ctx, code, state)
}

func (a *googleAdmin) Logout() error { return a.auth.Logout() }

func (a *googleAdmin) Status() (commands.GoogleStatus, error) {
	authorized, scopes, expiry, err := a.auth.Status()
	if err != nil {
		return commands.GoogleStatus{}, err
	}
	return commands.GoogleStatus{Authorized: authorized, Scopes: scopes, Expiry: expiry}, nil
}

// commandsView hands out an explicit nil interface when Google is absent, the
// same trap the MCP admin avoids: a nil pointer inside a non-nil interface
// would make the command surface believe the capability exists.
func (a *googleAdmin) commandsView() commands.GoogleRuntime {
	if a == nil {
		return nil
	}
	return a
}
