package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/plugins/webui"
)

// WebUIConfig holds what NewWebHandler needs beyond the config file path:
// the single owner login credential and the key used to sign session
// cookies (Eggy's existing EGGY_ENCRYPTION_KEY -- see
// docs/superpowers/specs/2026-07-22-web-config-ui-design.md), plus the chat
// wiring (docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md):
// ChatHub/Enqueue/Memory/OwnerID are only read by the /api/chat/* routes and
// may be left zero-valued in tests that only exercise login/config routes.
// MCPLoginStarter begins an OAuth authorization for one configured MCP
// server and returns the provider URL to send the owner to.
type MCPLoginStarter interface {
	BeginLogin(ctx context.Context, server string) (string, error)
}

type WebUIConfig struct {
	UserEmail  string
	Password   string
	SigningKey []byte
	Now        func() time.Time
	ChatHub    ChatStream
	Enqueue    func(context.Context, events.Event) error
	Memory     HistoryReader
	Threads    ThreadDirectory
	OwnerID    string
	// MCP is the running MCP manager, or nil when no server is configured.
	// The web panel edits MCP config through internal/config like every other
	// section; this is only the part config cannot do -- starting an OAuth
	// flow against a live connection.
	MCP MCPLoginStarter
	// TrustedProxyHops is how many reverse proxies Eggy is deployed behind
	// (server.trusted_proxy_hops). It is the login throttle's whole notion
	// of client identity: at 0 the throttle keys on RemoteAddr and
	// X-Forwarded-For is ignored, because a header anyone can spoof would
	// let an attacker mint a fresh throttle bucket per attempt. Set it to
	// the real hop count -- 1 behind Railway -- and the throttle keys on the
	// address that proxy observed instead of on the proxy itself.
	TrustedProxyHops int
}

const (
	webSessionCookie = "eggy_session"
	webSessionTTL    = 12 * time.Hour
)

type webResult struct {
	State        string     `json:"state"`
	Title        string     `json:"title,omitempty"`
	Detail       string     `json:"detail,omitempty"`
	Fields       []webField `json:"fields,omitempty"`
	TableHeaders []string   `json:"table_headers,omitempty"`
	TableRows    [][]string `json:"table_rows,omitempty"`
	Lines        []string   `json:"lines,omitempty"`
	Next         []string   `json:"next,omitempty"`
}

type webField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

const (
	webSuccess = "success"
	webInfo    = "info"
	webError   = "error"
)

// The two shapes the HTTP surface can take. The web app asks for this before
// anything else, because in safe mode every other route it would call is
// either absent or reporting the startup failure -- it needs to know which
// screen to render, not to discover it one failed request at a time. The probe
// is unauthenticated: it says whether Eggy started, which is already visible
// from /readyz, and nothing about why.
const (
	modeNormal = "normal"
	modeSafe   = "safe"
)

func writeMode(mode string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"mode":"` + mode + `"}`))
	}
}

// NewWebHandler serves Eggy's embedded web configuration UI and its small
// JSON API. Config routes call internal/config directly: the authenticated
// web panel is the runtime administration surface, while Telegram commands
// remain conversational.
// configPath may be empty in tests that only exercise login/session/logout.
func NewWebHandler(configPath string, webConfig WebUIConfig) http.Handler {
	now := webConfig.Now
	if now == nil {
		now = time.Now
	}
	throttle := webui.NewLoginThrottle(now)
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("GET /api/mode", writeMode(modeNormal))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, webResult{State: webSuccess, Title: "Session is valid."})
	}))

	for _, section := range []string{"providers", "models"} {
		mux.Handle("GET /api/config/"+section, requireWebSession(webConfig, now, webConfigGetRoute(configPath, section)))
		mux.Handle("POST /api/config/"+section, requireWebSession(webConfig, now, webConfigSetRoute(configPath, section)))
	}

	mux.Handle("GET /api/config/mcp", requireWebSession(webConfig, now, webMCPListRoute(configPath)))
	mux.Handle("POST /api/config/mcp", requireWebSession(webConfig, now, webMCPSetRoute(configPath)))
	mux.Handle("DELETE /api/config/mcp/{name}", requireWebSession(webConfig, now, webMCPRemoveRoute(configPath)))
	// Starting an OAuth flow is owner-only: an anonymous visitor who could
	// reach this would bind their own account as Eggy's credential for that
	// server. The matching callback is deliberately not session-gated -- it is
	// the provider's redirect, authenticated by the state parameter it carries.
	if webConfig.MCP != nil {
		mux.Handle("GET /auth/mcp/{server}", requireWebSession(webConfig, now, webMCPLoginRoute(webConfig.MCP)))
	}

	mux.Handle("GET /api/chat/threads", requireWebSession(webConfig, now, newThreadListHandler(webConfig.Threads)))
	mux.Handle("POST /api/chat/threads", requireWebSession(webConfig, now, newThreadCreateHandler(webConfig.Threads, now)))
	mux.Handle("GET /api/chat/threads/{id}/history", requireWebSession(webConfig, now, newThreadHistoryHandler(webConfig.Threads, webConfig.Memory)))
	mux.Handle("GET /api/chat/threads/{id}/stream", requireWebSession(webConfig, now, newThreadStreamHandler(webConfig.ChatHub, webConfig.Threads)))
	mux.Handle("POST /api/chat/threads/{id}/send", requireWebSession(webConfig, now, newThreadSendHandler(webConfig.Enqueue, webConfig.OwnerID, webConfig.Threads)))
	mux.Handle("POST /api/chat/approve", requireWebSession(webConfig, now, newChatApproveHandler(webConfig.Enqueue, webConfig.OwnerID)))

	return mux
}

func webMCPListRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		servers, err := config.GetMCPServersConfig(configPath)
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
			rows = append(rows, []string{name, server.Transport, server.URL, server.Auth, strconv.FormatBool(server.Enabled), server.BearerTokenEnv})
		}
		writeWebResult(w, webResult{
			State:        webSuccess,
			TableHeaders: []string{"Name", "Transport", "URL", "Auth", "Enabled", "Bearer token env"},
			TableRows:    rows,
		})
	}
}

func webMCPSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			Transport      string `json:"transport"`
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
		if err := config.SetMCPServer(configPath, config.MCPServerInput{
			Name: input.Name, URL: input.URL, Transport: input.Transport,
			Auth: input.Auth, BearerTokenEnv: input.BearerTokenEnv, Enabled: input.Enabled,
		}); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved MCP server " + input.Name + ".", Detail: "Restart Eggy for this to take effect."})
	}
}

// webMCPLoginRoute starts an OAuth flow and redirects the owner's browser to
// the provider. Without it the callback route below is unreachable: nothing
// else in the process calls BeginLogin, so an auth: oauth server could be
// configured but never authorized.
func webMCPLoginRoute(runtime MCPLoginStarter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server := r.PathValue("server")
		if server == "" {
			writeWebError(w, http.StatusBadRequest, "server name is required")
			return
		}
		authorizationURL, err := runtime.BeginLogin(r.Context(), server)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, authorizationURL, http.StatusFound)
	}
}

func webMCPRemoveRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			writeWebError(w, http.StatusBadRequest, "server name is required")
			return
		}
		if err := config.RemoveMCPServer(configPath, name); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Removed MCP server " + name + ".", Detail: "Restart Eggy for this to take effect."})
	}
}

func webConfigGetRoute(configPath, section string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result := webResult{State: webSuccess}
		switch section {
		case "providers":
			names := make([]string, 0, len(cfg.Providers))
			for name := range cfg.Providers {
				names = append(names, name)
			}
			sort.Strings(names)
			result.TableHeaders = []string{"Provider", "Adapter", "Base URL", "API key env"}
			for _, name := range names {
				provider := cfg.Providers[name]
				result.TableRows = append(result.TableRows, []string{name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv})
			}
		case "models":
			aliases := make([]string, 0, len(cfg.ModelAliases))
			for alias := range cfg.ModelAliases {
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			result.TableHeaders = []string{"Alias", "Provider", "Model", "Reasoning efforts"}
			for _, alias := range aliases {
				model := cfg.ModelAliases[alias]
				result.TableRows = append(result.TableRows, []string{alias, model.Provider, model.Model, strings.Join(model.ReasoningEfforts, ", ")})
			}
		}
		writeWebResult(w, result)
	}
}

func webConfigSetRoute(configPath, section string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var named map[string]string
		if err := json.NewDecoder(r.Body).Decode(&named); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		var err error
		var title string
		switch section {
		case "providers":
			err = config.SetProvider(configPath, named["name"], named["adapter"], named["base_url"], named["api_key_env"])
			title = "Set provider " + named["name"] + "."
		case "models":
			err = config.SetModelAlias(configPath, named["alias"], named["provider"], named["model"], named["reasoning_efforts"])
			title = "Set model " + named["alias"] + "."
		}
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: title, Detail: "Restart Eggy for this to take effect."})
	}
}

func handleWebLogin(webConfig WebUIConfig, throttle *webui.LoginThrottle, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, webConfig.TrustedProxyHops)
		// Refuse rather than sleep: sleeping inside the handler pins a
		// server goroutine per throttled attempt, which is a cheap way to
		// exhaust the process using nothing but wrong passwords.
		if delay := throttle.Delay(ip); delay > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((delay+time.Second-1)/time.Second)))
			writeWebError(w, http.StatusTooManyRequests, "too many failed login attempts, try again shortly")
			return
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
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged in."})
	}
}

func handleWebLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: "", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
		})
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged out."})
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

// clientIP identifies the client the login throttle counts against. With
// hops > 0 it walks X-Forwarded-For from the right, skipping the hops-1
// proxies Eggy is known to sit behind, so the returned address is the one the
// outermost trusted proxy actually observed -- entries further left are
// attacker-supplied and never used. Anything unexpected (no header, a chain
// shorter than the configured hop count, an unparseable entry) falls back to
// RemoteAddr rather than trusting a value that does not fit the deployment.
func clientIP(r *http.Request, hops int) string {
	remote := remoteHost(r)
	if hops <= 0 {
		return remote
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	index := len(forwarded) - hops
	if index < 0 {
		return remote
	}
	candidate := strings.TrimSpace(forwarded[index])
	if net.ParseIP(candidate) == nil {
		return remote
	}
	return candidate
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeWebResult(w http.ResponseWriter, result webResult) {
	body, err := json.Marshal(result)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, "failed to render response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatusForState(result.State))
	_, _ = w.Write(body)
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	body, _ := json.Marshal(webResult{State: webError, Title: message})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func httpStatusForState(state string) int {
	switch state {
	case webError:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}
