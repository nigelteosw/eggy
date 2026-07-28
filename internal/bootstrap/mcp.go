package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
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
		if err := mcpadapter.MigrateLegacyOAuthRecords(filepath.Join(layout.Root, "mcp"), layout.Auth()); err != nil {
			return nil, fmt.Errorf("migrate MCP OAuth records: %w", err)
		}
		oauthStore, err = mcpadapter.OpenOAuthStore(layout.Auth(), secrets.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("open MCP OAuth store: %w", err)
		}
	}
	return mcpadapter.NewManager(ctx, servers, mcpadapter.Options{HTTPClient: options.HTTPClient, OAuthStore: oauthStore, Now: options.Now})
}

// mcpCommands adapts the MCP manager to the command layer's own neutral
// types, so internal/commands never imports the adapter package. This is the
// only place that knows both sides exist, which is what the composition root
// is for.
type mcpCommands struct{ manager *mcpadapter.Manager }

// newMCPCommands returns a true nil interface when no manager was built,
// rather than a non-nil interface wrapping a nil pointer. That distinction is
// load-bearing: every handler guards on `service.mcp == nil` to report "MCP is
// not configured", and a boxed nil pointer passes that guard and then panics
// on the first method call.
func newMCPCommands(manager *mcpadapter.Manager) commands.MCPCommands {
	if manager == nil {
		return nil
	}
	return mcpCommands{manager: manager}
}

func mcpStatus(status mcpadapter.ServerStatus) commands.ToolServerStatus {
	return commands.ToolServerStatus{
		Name: status.Name, State: string(status.State), Tools: status.Tools,
		ReloadRequired: status.ReloadRequired, Warnings: status.Warnings,
		Diagnostic: status.Diagnostic,
	}
}

func (c mcpCommands) Statuses() []commands.ToolServerStatus {
	source := c.manager.Statuses()
	statuses := make([]commands.ToolServerStatus, 0, len(source))
	for _, status := range source {
		statuses = append(statuses, mcpStatus(status))
	}
	return statuses
}

func (c mcpCommands) Status(name string) (commands.ToolServerStatus, error) {
	status, err := c.manager.Status(name)
	if err != nil {
		return commands.ToolServerStatus{}, err
	}
	return mcpStatus(status), nil
}

func (c mcpCommands) Probe(ctx context.Context, name string) (commands.ToolServerProbe, error) {
	probe, err := c.manager.Probe(ctx, name)
	if err != nil {
		return commands.ToolServerProbe{}, err
	}
	return commands.ToolServerProbe{
		Server: probe.Server, State: string(probe.State), Tools: probe.Tools,
		Latency: probe.Latency, Diagnostic: probe.Diagnostic,
	}, nil
}

func (c mcpCommands) BeginLogin(ctx context.Context, name string) (string, error) {
	return c.manager.BeginLogin(ctx, name)
}
func (c mcpCommands) Logout(name string) error { return c.manager.Logout(name) }
func (c mcpCommands) Refresh(ctx context.Context, name string) error {
	return c.manager.Refresh(ctx, name)
}

func ExecuteMCPCLI(ctx context.Context, config config.Config, secrets config.Secrets, options AppOptions, args []string) (commands.CommandResult, bool, error) {
	manager, err := newMCPManager(ctx, config, secrets, options)
	if err != nil {
		return commands.CommandResult{}, false, err
	}
	if manager != nil {
		defer manager.Close()
	}
	service := commands.New(commands.Options{Config: config, MCP: newMCPCommands(manager)})
	return service.ExecuteCLI(ctx, args)
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
