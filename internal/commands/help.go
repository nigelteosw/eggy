package commands

import (
	"slices"
	"strings"
)

// One list supplies both the phone's autocomplete and the grouped overview.
// Dispatch stays explicit in Execute.
var telegramCommands = []struct {
	Name, Description, Group string
	NoArgs                   bool
}{
	{"menu", "Open Eggy's Telegram controls", "Conversation", true},
	{"settings", "Open your personal settings", "Personal settings", true},
	{"help", "Show commands or help for a topic", "Conversation", false},
	{"status", "Show brief operational status", "Conversation", true},
	{"stop", "Stop the turn in the current conversation", "Conversation", true},
	{"clear", "Clear recent history; preserve durable memory", "Conversation", true},
	{"soul", "Show Eggy's soul and how to change it", "Conversation", true},
	{"model", "Select your model, effort and thinking; browse shared models", "Personal settings", false},
	{"mcp", "Configure and authorize shared MCP servers", "Shared administration", false},
	{"web", "Send a one-tap sign-in link to the web panel", "Conversation", true},
	{"google", "Configure and authorize shared Google Workspace", "Shared administration", false},
	{"mode", "Set your approval mode: strict, normal or auto", "Personal settings", false},
	{"heartbeat", "Turn your check-ins on or off", "Personal settings", false},
	{"restart", "Reload shared config for everyone", "Shared administration", true},
}

const helpUsage = "Usage: /help [model|mcp|google|mode]"
const modelUsage = "Usage:\n/model [alias|default]\n/model effort [value|default]\n/model thinking [on|off]\n/model settings effort|thinking [value]\n/model providers\n/model available <provider> [filter]\n" + modelAddUsage
const modeUsage = "Usage: /mode [strict|normal|auto]"

// CommandNames is every command Telegram accepts when typed, including /code
// when the coding extension runs. The menu is a subset of it.
func CommandNames(code bool) map[string]bool {
	names := make(map[string]bool, len(telegramCommands)+1)
	for _, command := range telegramCommands {
		names[command.Name] = true
	}
	if code {
		names["code"] = true
	}
	return names
}

// menuCommands are the everyday few the phone's / menu offers. The rest still
// work when typed, and /help lists them.
var menuCommands = []string{"menu", "stop", "clear", "web", "settings", "help"}

// HelpText is the short overview. Shared administration is one line here,
// because a person who is not running Eggy's configuration does not need it
// listed beside their own commands.
func HelpText() string {
	var lines []string
	for _, group := range []string{"Conversation", "Personal settings"} {
		lines = append(lines, "**"+group+"**")
		for _, command := range telegramCommands {
			if command.Group == group {
				lines = append(lines, "/"+command.Name+" — "+command.Description)
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, "**Shared administration**", "/mcp, /google and /restart change things for everyone.", "")
	return strings.Join(lines, "\n") + "Details: /help model, /help mcp, /help google, /help mode"
}

func (s *CommandService) help(args []string) string {
	if len(args) == 0 {
		return HelpText()
	}
	if len(args) != 1 {
		return helpUsage
	}
	var text string
	switch args[0] {
	case "model":
		text = modelUsage + "\n\nModel, effort and thinking are personal. Adding models changes the shared catalog. Thinking shows provider-supplied reasoning; it does not enable reasoning. Aliases win over effort/thinking; use /model settings effort or /model settings thinking to disambiguate.\nExample: /model available openrouter sonnet\nNext: /model add <alias> <provider> <model>, then /restart and /model <alias>."
		if s.AgentRuntime == nil {
			text += "\nModel selection is unavailable until a model is configured."
		}
		if s.ModelDiscovery == nil {
			text += "\nProvider browsing is unavailable in this runtime."
		}
	case "mcp":
		text = mcpUsage() + "\n\nMCP configuration and authorization are shared with everyone. Use environment variable names, never secret values.\nExample: /mcp add tools url=https://tools.example.com/mcp\nNext: /restart, then /mcp; use /mcp login tools if authorization is needed."
		if s.MCP == nil {
			text += "\nRuntime login/logout are unavailable until an enabled server is running."
		}
	case "google":
		text = googleUsage() + "\n\nGoogle configuration and its identity are shared with everyone. First set google.expected_email to Eggy's Workspace identity in the web panel (/web).\nExample: /google set client_id=YOUR_DESKTOP_CLIENT_ID products=gmail\nNext: /restart, then /google login and paste the failed redirect URL to finish."
		if s.Google == nil {
			text += "\nRuntime status/login/logout are unavailable until Google is configured and running."
		}
	case "mode":
		text = modeUsage + "\n\nThis is personal; other accounts keep their own mode.\n" + ModeMessage("strict") + "\n" + ModeMessage("normal") + "\n" + ModeMessage("auto") + "\nYour private memory never asks, and an MCP server follows its own approval policy.\nExample: /mode normal\nNext: /mode to check your setting; /web to review pending approvals."
	default:
		return helpUsage
	}
	return text
}

// TelegramAutocomplete is the / menu: the everyday commands, and /code when the
// coding extension is running.
func TelegramAutocomplete(code bool) []struct{ Name, Description string } {
	result := make([]struct{ Name, Description string }, 0, len(menuCommands)+1)
	for _, command := range telegramCommands {
		if slices.Contains(menuCommands, command.Name) {
			result = append(result, struct{ Name, Description string }{command.Name, command.Description})
		}
	}
	if code {
		result = append(result, struct{ Name, Description string }{"code", "Run a coding task on an allowed repository"})
	}
	return result
}
