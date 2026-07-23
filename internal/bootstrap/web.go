package bootstrap

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/channels/webchat"
	"github.com/nigelteosw/eggy/internal/adapters/webui"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
)

// WebUIConfig holds what NewWebHandler needs beyond the config file path:
// the single owner login credential and the key used to sign session
// cookies (Eggy's existing EGGY_ENCRYPTION_KEY -- see
// docs/superpowers/specs/2026-07-22-web-config-ui-design.md), plus the chat
// wiring (docs/superpowers/specs/2026-07-23-web-chat-interface-design.md):
// ChatHub/Enqueue/Store/OwnerID are only read by the /api/chat/* routes and
// may be left zero-valued in tests that only exercise login/config routes.
type WebUIConfig struct {
	UserEmail  string
	Password   string
	SigningKey []byte
	Now        func() time.Time
	ChatHub    *webchat.Hub
	Enqueue    func(context.Context, events.Event) error
	Store      ports.StateStore
	OwnerID    string
}

const (
	webSessionCookie = "eggy_session"
	webSessionTTL    = 12 * time.Hour
)

// NewWebHandler serves Eggy's embedded web configuration UI and its small
// JSON API. Every /api/config/* route is a thin translation into the same
// CommandRequest/CommandResult shape Telegram and the CLI already use, so
// there is exactly one place config validation and mutation logic lives.
// configPath may be empty in tests that only exercise login/session/logout.
func NewWebHandler(configPath string, webConfig WebUIConfig) http.Handler {
	now := webConfig.Now
	if now == nil {
		now = time.Now
	}
	throttle := webui.NewLoginThrottle(now)
	commands := &CommandService{configPath: configPath}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Session is valid."})
	}))

	for _, section := range []struct {
		path     string
		get, set []string
	}{
		{"providers", []string{"config", "get", "providers"}, []string{"config", "set", "provider"}},
		{"models", []string{"config", "get", "models"}, []string{"config", "set", "model"}},
		{"calendar", []string{"config", "get", "calendar"}, []string{"config", "set", "calendar"}},
	} {
		mux.Handle("GET /api/config/"+section.path, requireWebSession(webConfig, now, webConfigGetRoute(commands, section.get)))
		mux.Handle("POST /api/config/"+section.path, requireWebSession(webConfig, now, webConfigSetRoute(commands, section.set)))
	}

	// MCP server definitions are file-owned by deliberate design (see
	// docs/superpowers/specs/2026-07-22-eggy-mcp-client-design.md): there is
	// no /config get|set mcp catalog command, so these routes call the
	// config_mutate.go helpers directly instead of bridging through
	// CommandService.
	mux.Handle("GET /api/config/mcp", requireWebSession(webConfig, now, webMCPListRoute(configPath)))
	mux.Handle("POST /api/config/mcp", requireWebSession(webConfig, now, webMCPSetRoute(configPath)))
	mux.Handle("DELETE /api/config/mcp/{name}", requireWebSession(webConfig, now, webMCPRemoveRoute(configPath)))

	mux.Handle("GET /api/chat/stream", requireWebSession(webConfig, now, newChatStreamHandler(webConfig.ChatHub)))
	mux.Handle("POST /api/chat/send", requireWebSession(webConfig, now, newChatSendHandler(webConfig.Enqueue, webConfig.OwnerID)))
	mux.Handle("POST /api/chat/approve", requireWebSession(webConfig, now, newChatApproveHandler(webConfig.Enqueue, webConfig.OwnerID)))
	mux.Handle("GET /api/chat/history", requireWebSession(webConfig, now, newChatHistoryHandler(webConfig.Store)))

	return mux
}

func webMCPListRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		servers, err := GetMCPServersConfig(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := make([][]string, 0, len(names))
		for _, name := range names {
			server := servers[name]
			rows = append(rows, []string{name, server.URL, server.Auth, strconv.FormatBool(server.Enabled), server.BearerTokenEnv})
		}
		writeWebResult(w, CommandResult{
			State:        ResultSuccess,
			TableHeaders: []string{"Name", "URL", "Auth", "Enabled", "Bearer token env"},
			TableRows:    rows,
		})
	}
}

func webMCPSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			Auth           string `json:"auth"`
			BearerTokenEnv string `json:"bearer_token_env"`
			Enabled        bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if input.Name == "" || input.URL == "" || input.Auth == "" {
			writeWebError(w, http.StatusBadRequest, "name, url, and auth are required")
			return
		}
		if err := SetMCPServer(configPath, input.Name, input.URL, input.Auth, input.BearerTokenEnv, input.Enabled); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Saved MCP server " + input.Name + ".", Detail: "Restart Eggy for this to take effect."})
	}
}

func webMCPRemoveRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			writeWebError(w, http.StatusBadRequest, "server name is required")
			return
		}
		if err := RemoveMCPServer(configPath, name); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Removed MCP server " + name + ".", Detail: "Restart Eggy for this to take effect."})
	}
}

func webConfigGetRoute(commands *CommandService, path []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := commands.dispatch(r.Context(), CommandRequest{Path: path})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, result)
	}
}

func webConfigSetRoute(commands *CommandService, path []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var named map[string]string
		if err := json.NewDecoder(r.Body).Decode(&named); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		result, err := commands.dispatch(r.Context(), CommandRequest{Path: path, Named: named})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebResult(w, result)
	}
}

func handleWebLogin(webConfig WebUIConfig, throttle *webui.LoginThrottle, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if delay := throttle.Delay(ip); delay > 0 {
			time.Sleep(delay)
		}
		var credentials struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if webConfig.UserEmail == "" || webConfig.Password == "" {
			writeWebError(w, http.StatusUnauthorized, "web UI login is not configured")
			return
		}
		if !constantTimeEqual(credentials.Email, webConfig.UserEmail) || !constantTimeEqual(credentials.Password, webConfig.Password) {
			throttle.RecordFailure(ip)
			writeWebError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		throttle.Reset(ip)
		expiresAt := now().Add(webSessionTTL)
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: webui.SignSession(webConfig.SigningKey, expiresAt),
			Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt,
		})
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Logged in."})
	}
}

func handleWebLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: "", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
		})
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Logged out."})
	}
}

func requireWebSession(webConfig WebUIConfig, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(webSessionCookie)
		if err != nil || !webui.VerifySession(webConfig.SigningKey, cookie.Value, now()) {
			writeWebError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeWebResult(w http.ResponseWriter, result CommandResult) {
	body, err := result.RenderJSON()
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatusForState(result.State))
	_, _ = w.Write(body)
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	body, _ := json.Marshal(CommandResult{State: ResultError, Title: message})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// httpStatusForState maps a CommandResult's classification to the HTTP
// status the web API returns.
func httpStatusForState(state ResultState) int {
	switch state {
	case ResultError, ResultHelp:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}
