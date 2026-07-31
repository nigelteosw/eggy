package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
)

// MCPRuntime is the running manager's half of MCP administration: the state a
// config file cannot know, and the OAuth flow. Config edits deliberately do
// not go through here -- they go to internal/config, the same helpers the web
// panel calls, so the two surfaces cannot disagree about what a valid server
// is.
type MCPRuntime interface {
	Statuses() []MCPStatus
	BeginLogin(ctx context.Context, server string) (string, error)
	Logout(server string) error
}

// MCPStatus is one server's live condition, flattened out of the adapter's own
// status type so this package stays free of the MCP plugin.
type MCPStatus struct {
	Name       string
	State      string
	Tools      int
	Diagnostic string
}

// restartNotice is the honest half of every config write from a chat surface.
// A write lands in config.yaml under the same lock the web panel uses, but
// adapters are built once at startup, so "configured" and "connected" disagree
// until a restart. Saying so beats a reply that reads like the server is live.
const restartNotice = "Restart Eggy to apply."

func (s *CommandService) mcpCommand(ctx context.Context, args []string) (string, bool, error) {
	if s.configPath == "" {
		return "MCP configuration is unavailable.", true, nil
	}
	if len(args) == 0 {
		return s.mcpList()
	}
	action, rest := args[0], args[1:]
	switch action {
	case "list":
		return s.mcpList()
	case "add":
		return s.mcpAdd(rest)
	case "remove":
		return s.mcpNamed(rest, "remove", func(name string) (string, error) {
			if err := config.RemoveMCPServer(s.configPath, name); err != nil {
				return "", err
			}
			return "Removed MCP server " + name + ". Stored credentials are kept. " + restartNotice, nil
		})
	case "enable", "disable":
		enabled := action == "enable"
		return s.mcpNamed(rest, action, func(name string) (string, error) {
			if err := config.SetMCPServerEnabled(s.configPath, name, enabled); err != nil {
				return "", err
			}
			return fmt.Sprintf("MCP server %s %sd. %s", name, action, restartNotice), nil
		})
	case "login":
		return s.mcpNamed(rest, "login", func(name string) (string, error) {
			if s.mcp == nil {
				return "", fmt.Errorf("no MCP server is running")
			}
			authorizationURL, err := s.mcp.BeginLogin(ctx, name)
			if err != nil {
				return "", err
			}
			return "Authorize " + name + " here, then come back:\n" + authorizationURL, nil
		})
	case "logout":
		return s.mcpNamed(rest, "logout", func(name string) (string, error) {
			if s.mcp == nil {
				return "", fmt.Errorf("no MCP server is running")
			}
			if err := s.mcp.Logout(name); err != nil {
				return "", err
			}
			return "Signed out of " + name + ". Use /mcp login " + name + " to authorize it again.", nil
		})
	default:
		return mcpUsage(), true, nil
	}
}

// mcpNamed runs an action that takes exactly one server name, so the arity
// check and the error rendering are written once. An error here is reported to
// the owner rather than returned: a mistyped server name is a normal outcome
// of a chat command, not a daemon fault.
func (s *CommandService) mcpNamed(args []string, action string, run func(string) (string, error)) (string, bool, error) {
	if len(args) != 1 {
		return "Usage: /mcp " + action + " <name>", true, nil
	}
	message, err := run(args[0])
	if err != nil {
		return fmt.Sprintf("Could not %s MCP server %s: %v", action, args[0], err), true, nil
	}
	return message, true, nil
}

func (s *CommandService) mcpList() (string, bool, error) {
	servers, err := config.GetMCPServersConfig(s.configPath)
	if err != nil {
		return "", true, err
	}
	if len(servers) == 0 {
		return "No MCP servers are configured.\n\n" + mcpUsage(), true, nil
	}
	live := map[string]MCPStatus{}
	if s.mcp != nil {
		for _, status := range s.mcp.Statuses() {
			live[status.Name] = status
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names)+1)
	lines = append(lines, "**MCP servers**")
	for _, name := range names {
		server := servers[name]
		detail := server.Transport
		if server.URL != "" {
			detail = server.URL
		}
		lines = append(lines, fmt.Sprintf("\n**%s** — %s\nauth: %s · %s", name, detail, server.Auth, mcpStateText(server.Enabled, live, name)))
	}
	return strings.Join(lines, "\n"), true, nil
}

// mcpStateText reports the running state, and is careful to distinguish a
// server the manager has never heard of from one it has connected. A server
// added since startup is configured but not running, and reporting it as
// merely "unavailable" would send the owner looking for a network fault.
func mcpStateText(enabled bool, live map[string]MCPStatus, name string) string {
	status, running := live[name]
	if !running {
		if !enabled {
			return "disabled"
		}
		return "not running — " + strings.ToLower(restartNotice)
	}
	text := status.State
	if status.Tools > 0 {
		text += fmt.Sprintf(" · %d tools", status.Tools)
	}
	if status.Diagnostic != "" {
		text += " · " + status.Diagnostic
	}
	if status.State == mcpStateLoginRequired {
		text += "\nRun /mcp login " + name
	}
	return text
}

// These mirror the adapter's own state strings. They are repeated rather than
// imported because internal/commands must not depend on a plugin package; the
// bootstrap adapter that converts between the two pins the values.
const (
	mcpStateLoginRequired = "login_required"
	mcpStateReady         = "ready"
)

// mcpAdd parses bounded named arguments. This is a command surface, not a YAML
// editor in a chat box: only the fields a surface may set are reachable, and
// no secret value is ever accepted -- bearer_env names an environment
// variable, it does not carry a token.
func (s *CommandService) mcpAdd(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "Usage: /mcp add <name> url=<https url> [auth=none|oauth|bearer-env] [transport=streamable-http] [bearer_env=VAR] [enabled=true|false]", true, nil
	}
	input := config.MCPServerInput{Name: args[0], Auth: "none", Enabled: true}
	for _, argument := range args[1:] {
		key, value, ok := strings.Cut(argument, "=")
		if !ok {
			return "Arguments after the name are key=value pairs. " + mcpUsage(), true, nil
		}
		switch key {
		case "url":
			input.URL = value
		case "transport":
			input.Transport = value
		case "auth":
			input.Auth = value
		case "bearer_env":
			input.BearerTokenEnv = value
		case "enabled":
			switch value {
			case "true":
				input.Enabled = true
			case "false":
				input.Enabled = false
			default:
				return "enabled must be true or false.", true, nil
			}
		default:
			return fmt.Sprintf("Unknown field %q. %s", key, mcpUsage()), true, nil
		}
	}
	if err := config.SetMCPServer(s.configPath, input); err != nil {
		return fmt.Sprintf("Could not save MCP server %s: %v", input.Name, err), true, nil
	}
	message := "Saved MCP server " + input.Name + ". " + restartNotice
	switch input.Auth {
	case "oauth":
		message += "\nThen run /mcp login " + input.Name + " to authorize it."
	case "bearer-env":
		// The name is config; the value is not. Setting one from chat cannot
		// create the other, and a server whose variable is unset will fail to
		// connect with no obvious cause unless this is said here.
		message += fmt.Sprintf("\nSet %s in the deployment's environment — Eggy reads the token from there, never from chat.", input.BearerTokenEnv)
	}
	return message, true, nil
}

func mcpUsage() string {
	return strings.Join([]string{
		"Usage:",
		"/mcp — list configured servers and their state",
		"/mcp add <name> url=<https url> [auth=none|oauth|bearer-env] [transport=streamable-http] [bearer_env=VAR] [enabled=true|false]",
		"/mcp remove|enable|disable|login|logout <name>",
		"",
		"stdio servers are edited in config.yaml: a subprocess command line is not a chat argument.",
	}, "\n")
}
