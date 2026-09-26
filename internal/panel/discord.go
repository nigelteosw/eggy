// The Discord card: the one connection whose credential is set from the
// panel rather than the deployment environment. The token goes into the
// sealed connection credential store; config.yaml only records that Discord
// is on and which application it is.
package panel

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nigelteosw/eggy/internal/auth/connections"
	"github.com/nigelteosw/eggy/internal/config"
)

// ConnectionCredentials is the sealed per-connection secret store the
// panel writes bot credentials into. *connections.Store implements it.
type ConnectionCredentials interface {
	Read(connection string) (connections.Credentials, error)
	Write(connection string, values connections.Credentials) error
}

// discordBotTokenField is the credential field the Discord adapter reads.
const discordBotTokenField = "bot_token"

type discordView struct {
	State         string `json:"state"`
	Enabled       bool   `json:"enabled"`
	ApplicationID string `json:"application_id"`
	// BotTokenSet says whether a token exists; the token itself never leaves
	// the store. BotTokenSource is "stored" for one set from the panel or
	// "environment" for the DISCORD_BOT_TOKEN override, so the owner can tell
	// why clearing the stored one would not stop the bot.
	BotTokenSet    bool   `json:"bot_token_set"`
	BotTokenSource string `json:"bot_token_source,omitempty"`
	// Running reports whether this process has a live Discord connection;
	// false with everything set means a restart is pending.
	Running bool `json:"running"`
}

func discordGetRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		view := discordView{State: webSuccess, Enabled: cfg.DiscordEnabled(), ApplicationID: cfg.Discord.ApplicationID, Running: webConfig.DiscordRunning}
		if webConfig.Connections != nil {
			credentials, err := webConfig.Connections.Read(DiscordConnection)
			if err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if credentials[discordBotTokenField] != "" {
				view.BotTokenSet, view.BotTokenSource = true, "stored"
			}
		}
		if !view.BotTokenSet && strings.TrimSpace(webConfig.getenv()(config.DiscordBotTokenEnv)) != "" {
			view.BotTokenSet, view.BotTokenSource = true, "environment"
		}
		writeJSON(w, view)
	}
}

// discordSetRoute saves the section and, when a token was typed, seals it.
// A blank token leaves the stored one alone, so re-saving the form never
// erases a credential; clearing is its own route. Enabling with no token
// anywhere is refused here rather than discovered as a silent non-start.
func discordSetRoute(configPath string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Enabled       bool   `json:"enabled"`
			ApplicationID string `json:"application_id"`
			BotToken      string `json:"bot_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		token := strings.TrimSpace(input.BotToken)
		if token != "" {
			if webConfig.Connections == nil {
				writeWebError(w, http.StatusServiceUnavailable, "bot credentials cannot be stored: set EGGY_ENCRYPTION_KEY and restart Eggy")
				return
			}
			if err := webConfig.Connections.Write(DiscordConnection, connections.Credentials{discordBotTokenField: token}); err != nil {
				writeWebError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if input.Enabled && token == "" && !discordTokenPresent(webConfig) {
			writeWebError(w, http.StatusBadRequest, "paste the bot token before enabling Discord")
			return
		}
		if err := config.SetDiscord(configPath, input.Enabled, input.ApplicationID); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		title := "Discord enabled."
		if !input.Enabled {
			title = "Discord disabled."
		}
		writeWebResult(w, webResult{State: webSuccess, Title: title, Detail: restartToApply})
	}
}

func discordTokenClearRoute(webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if webConfig.Connections == nil {
			writeWebError(w, http.StatusNotFound, "no stored credentials")
			return
		}
		if err := webConfig.Connections.Write(DiscordConnection, connections.Credentials{discordBotTokenField: ""}); err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Bot token removed.", Detail: restartToApply})
	}
}

func discordTokenPresent(webConfig WebUIConfig) bool {
	if webConfig.Connections != nil {
		if credentials, err := webConfig.Connections.Read(DiscordConnection); err == nil && credentials[discordBotTokenField] != "" {
			return true
		}
	}
	return strings.TrimSpace(webConfig.getenv()(config.DiscordBotTokenEnv)) != ""
}
