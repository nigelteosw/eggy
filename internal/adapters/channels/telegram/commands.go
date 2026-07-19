package telegram

// Commands is the single source of truth for Telegram's autocomplete
// suggestions. Bootstrap's CommandService dispatch must handle every name
// listed here, so the two surfaces cannot drift apart.
func Commands() []BotCommand {
	return []BotCommand{
		{Name: "status", Description: "Show operational status (no model tokens used)"},
		{Name: "repositories", Description: "List configured repositories"},
		{Name: "runs", Description: "List Codex coding runs"},
		{Name: "stop", Description: "Stop a running Codex run: /stop <run-id>"},
		{Name: "schedules", Description: "List scheduled instructions"},
		{Name: "memory", Description: "Show durable memory"},
		{Name: "model", Description: "Show or change the active model alias"},
		{Name: "usage", Description: "Show or reset local token usage counters"},
		{Name: "new", Description: "Start a new conversation"},
		{Name: "calendar_auth", Description: "Start Google Calendar enrollment"},
	}
}
