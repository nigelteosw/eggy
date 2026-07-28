package commands

import (
	"context"
	"fmt"
	"strings"
)

// mcpExplanation is the one place that explains what MCP is and how to add a
// server, shown by /help mcp, eggy help mcp, and any /mcp command run before
// a server is configured.
const mcpExplanation = "MCP (Model Context Protocol) lets Eggy call tools exposed by a remote server, such as Railway, directly in conversation. Servers are defined in config.yaml's mcp.servers map, not through /mcp or the CLI — see the mcp.servers.railway block in config.example.yaml for a complete example, including the URL, auth mode, and tool filter. After adding a server: an oauth server needs EGGY_ENCRYPTION_KEY set so its credentials can be stored encrypted, a bearer-env server needs its named token environment variable set, then restart Eggy either way. An oauth server also needs /mcp login <server> once Eggy is back up; a bearer-env or none server is ready to use immediately. Once a server is configured: /mcp lists servers and status, /mcp status <server> and /mcp probe <server> inspect one, /mcp login <server> and /mcp logout <server> manage OAuth, and /mcp reload <server> reconnects it and re-reads its tool catalog. Only adding or editing a server needs a restart; reconnecting, reloading, logging in, and logging out all take effect on the next turn."

func mcpNotConfigured() CommandResult {
	return CommandResult{State: ResultInfo, Title: "MCP is not configured.", Detail: mcpExplanation}
}

func handleMCP(_ context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	if service.mcp == nil {
		return mcpNotConfigured(), nil
	}
	if len(request.Args) > 0 {
		return usageHelp(mustEntry("mcp"), fmt.Sprintf("Unknown MCP subcommand %q. Servers are added and removed by editing config.yaml's mcp.servers map (see config.example.yaml and /help mcp), not through this command. Configured servers support: status, probe, login, logout, reload.", request.Args[0])), nil
	}
	statuses := service.mcp.Statuses()
	rows := make([][]string, 0, len(statuses))
	for _, status := range statuses {
		rows = append(rows, []string{status.Name, string(status.State), fmt.Sprintf("%d", status.Tools), fmt.Sprintf("%t", status.ReloadRequired)})
	}
	return CommandResult{Title: "MCP servers", TableHeaders: []string{"Server", "State", "Tools", "Reload required"}, TableRows: rows}, nil
}

func handleMCPStatus(_ context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	name, result := mcpServerArg(service, request, "mcp status")
	if result != nil {
		return *result, nil
	}
	status, err := service.mcp.Status(name)
	if err != nil {
		return errorResult(err), nil
	}
	fields := []ResultField{{Label: "State", Value: string(status.State)}, {Label: "Tools", Value: fmt.Sprintf("%d", status.Tools)}, {Label: "Reload required", Value: fmt.Sprintf("%t", status.ReloadRequired)}}
	if len(status.Warnings) > 0 {
		fields = append(fields, ResultField{Label: "Warnings", Value: strings.Join(status.Warnings, "; ")})
	}
	if status.Diagnostic != "" {
		fields = append(fields, ResultField{Label: "Diagnostic", Value: status.Diagnostic})
	}
	return CommandResult{Title: "MCP server " + name, Fields: fields}, nil
}

func handleMCPProbe(ctx context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	name, result := mcpServerArg(service, request, "mcp probe")
	if result != nil {
		return *result, nil
	}
	probe, err := service.mcp.Probe(ctx, name)
	if err != nil {
		return errorResult(err), nil
	}
	fields := []ResultField{{Label: "State", Value: string(probe.State)}, {Label: "Tools", Value: fmt.Sprintf("%d", probe.Tools)}, {Label: "Latency", Value: probe.Latency.String()}}
	if probe.Diagnostic != "" {
		fields = append(fields, ResultField{Label: "Diagnostic", Value: probe.Diagnostic})
	}
	return CommandResult{Title: "MCP probe " + name, Fields: fields}, nil
}

func handleMCPLogin(ctx context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	name, result := mcpServerArg(service, request, "mcp login")
	if result != nil {
		return *result, nil
	}
	authorizationURL, err := service.mcp.BeginLogin(ctx, name)
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "MCP login started for " + name + ".", Detail: "Open the authorization URL and approve the intended account or workspace.", Fields: []ResultField{{Label: "Authorization URL", Value: authorizationURL}}}, nil
}

func handleMCPLogout(_ context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	name, result := mcpServerArg(service, request, "mcp logout")
	if result != nil {
		return *result, nil
	}
	if err := service.mcp.Logout(name); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Logged out of MCP server " + name + ".", Detail: "Its tools were removed from the active catalog. Run /mcp login " + name + " to restore them."}, nil
}

// handleMCPReload reloads one server in place. A server that is unreachable,
// or whose session died, is reconnected as part of the reload, so this is
// also the repair path for a server that was down when Eggy started.
func handleMCPReload(ctx context.Context, service *CommandService, request CommandRequest) (CommandResult, error) {
	name, result := mcpServerArg(service, request, "mcp reload")
	if result != nil {
		return *result, nil
	}
	if _, err := service.mcp.Status(name); err != nil {
		return errorResult(err), nil
	}
	if err := service.mcp.Refresh(ctx, name); err != nil {
		return errorResult(err), nil
	}
	status, err := service.mcp.Status(name)
	if err != nil {
		return errorResult(err), nil
	}
	fields := []ResultField{{Label: "State", Value: string(status.State)}, {Label: "Tools", Value: fmt.Sprintf("%d", status.Tools)}}
	if len(status.Warnings) > 0 {
		fields = append(fields, ResultField{Label: "Warnings", Value: strings.Join(status.Warnings, "; ")})
	}
	return CommandResult{Title: "Reloaded MCP server " + name + ".", Detail: "Its tools take effect on the next turn.", Fields: fields}, nil
}

func mcpServerArg(service *CommandService, request CommandRequest, path string) (string, *CommandResult) {
	if service.mcp == nil {
		result := mcpNotConfigured()
		return "", &result
	}
	if len(request.Args) != 1 {
		result := usageHelp(mustEntry(path), "Expected exactly one configured MCP server name.")
		return "", &result
	}
	return request.Args[0], nil
}
