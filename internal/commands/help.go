package commands

import (
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
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

const soulDescription = "Show Eggy's soul and how to change it"

const helpUsage = "Usage: /help [model|mode|checkins|admin|mcp|google]"
const modelUsage = "Usage:\n/model [alias|default]\n/model effort [value|default]\n/model thinking [on|off]\n/model providers\n/model available <provider> [filter]\n" + modelAddUsage
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

// HelpText is the short overview: what a person does most, in my voice, with
// the syntax for everything else one /help <topic> away.
func HelpText() string {
	return strings.Join([]string{
		"Just talk to me -- that's most of it. A few commands help:",
		"",
		"/menu -- my controls, as buttons",
		"/stop -- stop what I'm doing in the current conversation",
		"/clear -- forget recent messages; I keep durable memory",
		"/web -- a one-tap sign-in link to the web panel",
		"/settings -- your model, check-ins and when I ask",
		"/status -- how I'm running",
		"",
		"More: /help model, /help mode, /help checkins, /help admin",
	}, "\n")
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
		text = modelUsage + "\n\nModel, effort and thinking are personal. Adding models changes the shared catalog. Thinking shows provider-supplied reasoning; it does not enable reasoning.\nExample: /model available openrouter sonnet\nNext: /model add <alias> <provider> <model>, then /restart and /model <alias>."
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
		text = modeUsage + "\n\nWhen I ask is personal; other accounts keep their own.\n/mode strict -- " + ModeLabel("strict") + ". " + ports.ModeStrict.Describe() + "\n/mode normal -- " + ModeLabel("normal") + ". " + ports.ModeNormal.Describe() + "\n/mode auto -- " + ModeLabel("auto") + ". " + ports.ModeAuto.Describe() + "\nYour private memory never asks, and an MCP server follows its own approval policy.\nExample: /mode normal\nNext: /mode to check your setting; /web to review pending approvals."
	case "checkins", "heartbeat":
		text = heartbeatUsage + "\n\nCheck-ins are personal and off until you turn them on. When they're on, I look at your watch list on the deployment's cadence and message you only when something needs you. How often I check, and between which hours, is shared and set in the web panel (Settings → Check-ins).\nExample: /heartbeat on\nNext: tell me what you're waiting on, and I'll add it to your watch list."
	case "admin":
		lines := []string{"These change things for everyone who uses me:", ""}
		for _, command := range telegramCommands {
			if command.Group == "Shared administration" {
				lines = append(lines, "/"+command.Name+" -- "+command.Description)
			}
		}
		text = strings.Join(lines, "\n") + "\n/soul -- " + soulDescription + "\n\nExample: /help mcp\nNext: /help mcp or /help google for their syntax."
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
