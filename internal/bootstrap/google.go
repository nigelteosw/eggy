package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/kernel/services"
	"github.com/nigelteosw/eggy/internal/ports"
	googleadapter "github.com/nigelteosw/eggy/plugins/tools/google"
)

// defaultGoogleScopes covers the products Eggy exposes, and nothing beyond
// them. They are requested together because one desktop client holds one
// grant: narrowing later means re-consenting, so the set is chosen once at
// login from the products actually configured.
//
// Drive, Docs and Contacts ask for write access because their tools write.
// The readonly variants they used to request are still reachable by setting
// google.scopes by hand, which is the supported way to run a read-only
// grant -- the tools then fail on write with Google's own 403 rather than
// pretending the capability is absent.
var defaultGoogleScopes = map[string]string{
	"gmail":    "https://www.googleapis.com/auth/gmail.modify",
	"calendar": "https://www.googleapis.com/auth/calendar",
	"drive":    "https://www.googleapis.com/auth/drive",
	"docs":     "https://www.googleapis.com/auth/documents",
	"sheets":   "https://www.googleapis.com/auth/spreadsheets",
	"contacts": "https://www.googleapis.com/auth/contacts",
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

// googleApprovals resolves config into the gated action set per tool.
//
// Omitted, it gates everything that writes. That default is the one an owner
// who never read this far should get: Eggy can read their mail without asking
// and cannot send any without being asked. An explicitly empty list means the
// owner turned the gates off deliberately, which is why nil and empty are not
// allowed to collapse into each other anywhere along this path.
func googleApprovals(cfg config.GoogleConfig) (map[string][]string, error) {
	if cfg.RequireApproval == nil {
		return googleadapter.Mutations(), nil
	}
	actions := googleadapter.Actions()
	gated := map[string][]string{}
	for _, entry := range cfg.RequireApproval {
		product, action, qualified := strings.Cut(strings.TrimSpace(entry), ".")
		// The product is checked before anything is said about its actions, so
		// a typo in the product name is reported as one rather than as advice
		// about the actions of a product that does not exist.
		if _, exists := defaultGoogleScopes[product]; !exists {
			return nil, fmt.Errorf("google.require_approval %q names no Google product", entry)
		}
		tool := "google_" + product
		known := actions[tool]
		switch {
		case !qualified:
			// Gating a product whole has to be asked for as product.*, never
			// inferred from a bare name. The bare form reads like "gate gmail",
			// which almost always means the writes -- and silently gating
			// reading the calendar to change one event is how an owner learns
			// to approve without looking.
			return nil, fmt.Errorf("google.require_approval %q must name an action, as in %q, or %q for all of them", entry, product+"."+known[0], product+"."+googleadapter.GateAll)
		case action == googleadapter.GateAll:
			gated[tool] = []string{googleadapter.GateAll}
			continue
		case !slices.Contains(known, action):
			return nil, fmt.Errorf("google.require_approval %q: %s has no action %q (it has %s)", entry, product, action, strings.Join(known, ", "))
		}
		// A product already gated whole cannot be narrowed by a later entry:
		// the wider instruction is the safe one to keep.
		if slices.Contains(gated[tool], googleadapter.GateAll) {
			continue
		}
		if slices.Contains(gated[tool], action) {
			return nil, fmt.Errorf("google.require_approval has duplicate entry %q", entry)
		}
		gated[tool] = append(gated[tool], action)
	}
	return gated, nil
}

// gateGoogleTools wraps each tool whose config names any gated action. A tool
// with none is left exactly as it was -- unwrapped, with its description
// unchanged -- so an owner who gates nothing pays no prompt bytes for the
// mechanism.
func gateGoogleTools(tools []ports.Tool, gated map[string][]string, requester services.ApprovalRequester, auto services.AutoApprover) []ports.Tool {
	for index, tool := range tools {
		actions := gated[tool.Definition().Name]
		if len(actions) == 0 {
			continue
		}
		rule := services.ApprovalRule{Gated: googleadapter.GateFor(actions), Notice: googleadapter.GateNotice(actions)}
		tools[index] = services.NewApprovalGatedToolIf(tool, requester, auto, rule)
	}
	return tools
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
