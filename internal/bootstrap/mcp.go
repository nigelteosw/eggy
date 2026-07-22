package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	mcpadapter "github.com/nigelteosw/eggy/internal/adapters/tools/mcp"
)

func newMCPManager(ctx context.Context, config Config, secrets Secrets, options AppOptions) (*mcpadapter.Manager, error) {
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
			Name: name, URL: configured.URL, RedirectURL: strings.TrimRight(config.Server.PublicBaseURL, "/") + "/auth/mcp/" + name + "/callback",
			Auth: configured.Auth, BearerToken: secrets.MCPBearerTokens[name], OAuthScopes: append([]string(nil), configured.OAuthScopes...),
			Enabled: configured.Enabled, ConnectTimeout: configured.ConnectTimeout.Value(), Timeout: configured.Timeout.Value(), MaxOutputBytes: configured.MaxOutputBytes,
			SupportsParallelToolCalls: configured.SupportsParallelToolCalls,
			Filter:                    mcpadapter.ToolFilter{Include: append([]string(nil), configured.ToolFilter.Include...), Exclude: append([]string(nil), configured.ToolFilter.Exclude...)},
		})
	}
	if options.FakeAdapters {
		return mcpadapter.NewFakeManager(servers)
	}
	var oauthStore *mcpadapter.OAuthStore
	if needsOAuthStore {
		var err error
		oauthStore, err = mcpadapter.OpenOAuthStore(config.DataDir, secrets.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("open MCP OAuth store: %w", err)
		}
	}
	return mcpadapter.NewManager(ctx, servers, mcpadapter.Options{HTTPClient: options.HTTPClient, OAuthStore: oauthStore, Now: options.Now})
}

func mcpCallbackHandler(manager *mcpadapter.Manager, restart func()) http.Handler {
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
		_, _ = response.Write([]byte("MCP authorization complete. Eggy is restarting.\n"))
		if restart != nil {
			restart()
		}
	})
}
