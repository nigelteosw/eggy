package telegram

// Commands is the single source of truth for Telegram's autocomplete
// suggestions. Bootstrap's CommandService dispatch must handle every name
// listed here, so the two surfaces cannot drift apart.
func Commands() []BotCommand {
	return []BotCommand{
		{Name: "status", Description: "Show operational status (no model tokens used)"},
		{Name: "start", Description: "Show the welcome message"},
		{Name: "help", Description: "Show available commands or usage for a specific command"},
		{Name: "repositories", Description: "List, add, or remove configured repositories"},
		{Name: "runs", Description: "List coding-agent runs"},
		{Name: "continue", Description: "Resume a coding session: /continue [run-id]"},
		{Name: "stop", Description: "Stop a coding-agent run: /stop <run-id>"},
		{Name: "schedules", Description: "List scheduled instructions"},
		{Name: "memory", Description: "Show durable memory"},
		{Name: "model", Description: "Show or change the active model alias"},
		{Name: "config", Description: "Get or set configuration sections"},
		{Name: "prompts", Description: "Manage custom prompts"},
		{Name: "usage", Description: "Show or reset local token usage counters"},
		{Name: "clear", Description: "Clear the context window"},
		{Name: "calendar_auth", Description: "Start Google Calendar enrollment"},
	}
}
