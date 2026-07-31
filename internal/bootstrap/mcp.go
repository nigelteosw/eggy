package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
	"github.com/nigelteosw/eggy/internal/web"
	mcpadapter "github.com/nigelteosw/eggy/plugins/tools/mcp"
)

func newMCPManager(ctx context.Context, config config.Config, secrets config.Secrets, options AppOptions) (*mcpadapter.Manager, error) {
	if len(config.MCP.Servers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(config.MCP.Servers))
	for name := range config.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]mcpadapter.ServerConfig, 0, len(names))
	needsOAuthStore := false
	for _, name := range names {
		configured := config.MCP.Servers[name]
		if configured.Enabled && configured.Auth == "oauth" {
			needsOAuthStore = true
		}
		servers = append(servers, mcpadapter.ServerConfig{
			Name: name, Transport: configured.Transport, URL: configured.URL,
			Command: configured.Command, Args: append([]string(nil), configured.Args...),
			EnvAllowlist: append([]string(nil), configured.EnvAllowlist...),
			RedirectURL:  strings.TrimRight(config.Server.PublicBaseURL, "/") + "/auth/mcp/" + name + "/callback",
			Auth:         configured.Auth, BearerToken: secrets.MCPBearerTokens[name], OAuthScopes: append([]string(nil), configured.OAuthScopes...),
			OAuthClientID: configured.OAuthClientID, OAuthClientSecret: secrets.MCPOAuthClientSecrets[name],
			Enabled: configured.Enabled, ConnectTimeout: configured.ConnectTimeout.Value(), Timeout: configured.Timeout.Value(), MaxOutputBytes: configured.MaxOutputBytes,
			SupportsParallelToolCalls: configured.SupportsParallelToolCalls,
			FailureThreshold:          configured.FailureThreshold, Cooldown: configured.Cooldown.Value(),
			Filter: mcpadapter.ToolFilter{Include: append([]string(nil), configured.ToolFilter.Include...), Exclude: append([]string(nil), configured.ToolFilter.Exclude...)},
		})
	}
	if options.FakeAdapters {
		return mcpadapter.NewFakeManager(servers)
	}
	var oauthStore *mcpadapter.OAuthStore
	if needsOAuthStore {
		var err error
		layout := home.At(config.DataDir)
		oauthStore, err = mcpadapter.OpenOAuthStore(layout.Auth(), secrets.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("open MCP OAuth store: %w", err)
		}
	}
	return mcpadapter.NewManager(ctx, servers, mcpadapter.Options{HTTPClient: options.HTTPClient, OAuthStore: oauthStore, Now: options.Now})
}

// mcpCallbackHandler completes an OAuth flow. CompleteLogin connects the
// server as part of storing the credentials, so a finished login makes its
// tools available on the next turn without restarting the process.
func mcpCallbackHandler(manager *mcpadapter.Manager) http.Handler {
	if manager == nil {
		return nil
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("error") != "" {
			http.Error(response, "MCP authorization was denied", http.StatusBadRequest)
			return
		}
		server := request.PathValue("server")
		code := request.URL.Query().Get("code")
		state := request.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(response, "missing MCP authorization response", http.StatusBadRequest)
			return
		}
		if err := manager.CompleteLogin(request.Context(), server, code, state); err != nil {
			http.Error(response, "MCP authorization failed", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("MCP authorization complete. Its tools are available on the next turn.\n"))
	})
}

// mcpAdmin adapts the MCP manager to the narrow views the owner surfaces
// need. It exists so internal/commands and internal/web can administer MCP
// without importing the plugin package, and it is the one place the adapter's
// state strings are translated -- both consumers pin the same values.
type mcpAdmin struct{ manager *mcpadapter.Manager }

// newMCPAdmin returns a true nil interface when no manager exists, never a nil
// pointer boxed into a non-nil interface: the surfaces check for nil to decide
// whether MCP administration is available at all.
func newMCPAdmin(manager *mcpadapter.Manager) *mcpAdmin {
	if manager == nil {
		return nil
	}
	return &mcpAdmin{manager: manager}
}

func (a *mcpAdmin) Statuses() []commands.MCPStatus {
	statuses := a.manager.Statuses()
	out := make([]commands.MCPStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, commands.MCPStatus{
			Name: status.Name, State: string(status.State),
			Tools: status.Tools, Diagnostic: status.Diagnostic,
		})
	}
	return out
}

func (a *mcpAdmin) BeginLogin(ctx context.Context, server string) (string, error) {
	return a.manager.BeginLogin(ctx, server)
}

func (a *mcpAdmin) Logout(server string) error { return a.manager.Logout(server) }

// commandsView and webView hand out the interface each surface declares. They
// return an explicit nil so a nil *mcpAdmin never reaches a consumer as a
// non-nil interface wrapping a nil pointer -- the same trap the Telegram
// channel avoids in app.go.
func (a *mcpAdmin) commandsView() commands.MCPRuntime {
	if a == nil {
		return nil
	}
	return a
}

func (a *mcpAdmin) webView() web.MCPLoginStarter {
	if a == nil {
		return nil
	}
	return a
}
