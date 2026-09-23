package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/auth/session"
	"github.com/nigelteosw/eggy/plugins/webui"
)

// WebUIConfig holds what NewWebHandler needs beyond the config file path:
// the credential store and the environment login binding every session
// starts from, plus the chat wiring
// (docs/superpowers/specs/2026-07-23-multi-thread-web-chat-design.md):
// ChatHub/Enqueue/Memory are only read by the /api/chat/* routes and may be
// left zero-valued in tests that only exercise login/config routes.
// MCPLoginStarter begins an OAuth authorization for one configured MCP
// server and returns the provider URL to send the owner to.
type MCPLoginStarter interface {
	BeginLogin(ctx context.Context, server string) (string, error)
}

type WebUIConfig struct {
	Now func() time.Time
	// Auth, Sessions and Accounts are the login's three authorities: the
	// credential and link records, the session rows, and the configured
	// membership. All three are required for any way in to exist; with any
	// missing the panel reports login as unavailable.
	Auth     ports.AccountAuthStore
	Sessions SessionStore
	Accounts AccountDirectory
	// PasswordAccountID is the account bound to the environment credentials
	// when this process started; EnvironmentAlias is the username that
	// stands for it, and EnvironmentPasswordHash the configured password
	// prehashed into the stored-password format so a wrong guess against it
	// costs what a wrong local password does. None of the three is
	// persisted, and the hash is never sent anywhere.
	PasswordAccountID       string
	EnvironmentAlias        string
	EnvironmentPasswordHash string
	InitializeAccount       func(string) error
	// IdentityLinks mints and cancels the single-use tokens that bind a
	// chat sender to an account, for every connection. Nil when no chat
	// connection is enabled.
	IdentityLinks       IdentityLinkStore
	TelegramBotUsername string
	// DiscordLinking mounts the Discord link routes; DiscordApplicationID
	// lets the card offer a direct DM link; DiscordRunning reports whether
	// this process holds a live bot connection.
	DiscordLinking       bool
	DiscordApplicationID string
	DiscordRunning       bool
	// Connections is the sealed credential store bot tokens are saved into
	// from the panel. Nil without an encryption key, in which case the
	// Discord card explains what to set.
	Connections ConnectionCredentials
	// PublicBaseURL is the deployment's public origin, which the CSRF
	// same-origin check accepts alongside the request's own Host.
	PublicBaseURL string
	ChatHub       ChatStream
	Enqueue       func(context.Context, events.Event) error
	Memory        HistoryReader
	Threads       ThreadDirectory
	// MCP is the running MCP manager, or nil when no server is configured.
	// The web panel edits MCP config through internal/config like every other
	// section; this is only the part config cannot do -- starting an OAuth
	// flow against a live connection.
	MCP MCPLoginStarter
	// Tools is the live tool registry, listed read-only by the panel.
	Tools ToolCatalog
	// Approvals lists what is waiting on the owner. Deciding one goes
	// through the existing chat approve route, not a second path.
	Approvals ApprovalDirectory
	// ApprovalMode is which of the three approval modes is in force, the
	// panel's half of the /mode command.
	ApprovalMode ApprovalModeSwitch
	// Agent is the model and reasoning-effort selection behind /model, which
	// the chat composer reads and writes. Nil leaves those routes answering
	// 404 and the composer drawing no model controls.
	Agent AgentSwitch
	// GoogleActions is what each Google product can do, keyed by product.
	// Bootstrap fills it from the adapter, because the panel must not carry a
	// second copy of a list that changes whenever a product gains an action --
	// and because a require_approval entry naming an action that does not
	// exist fails at startup, which for a config edit means the owner lands in
	// safe mode over a typo the form could have refused.
	GoogleActions map[string]GoogleProductActions
	// GoogleConnection reports whose account the shared grant is, so the
	// card can show the verified address against the expected one. Nil when
	// Google is disabled.
	GoogleConnection GoogleConnectionReader
	// ModelDiscovery browses a provider's catalog so the models card can offer
	// what is on sale instead of asking the owner to type an ID from memory.
	// Nil leaves the route answering 404 and the card's browse control absent,
	// which is also what every provider opting out of discovery produces.
	ModelDiscovery ModelDiscoverer
	// Documents are the context documents the panel edits: the watch list and
	// SOUL.md. Nil leaves their routes answering 404 and the cards absent.
	Documents ContextDocuments
	// Traces is the recorded turn log: every model call with the prompt that
	// produced it, every tool call with its arguments and output. Nil when
	// tracing is switched off, which leaves the routes unmounted and the
	// panel's Traces view absent rather than empty.
	Traces TraceDirectory
	// Schedules lists and cancels cron jobs for the panel. Creating one
	// stays conversational, so this is deliberately not a full CRUD surface.
	Schedules ScheduleDirectory
	// Restarter rebuilds the daemon around the config on disk, the panel's
	// half of /restart. Every write this package acknowledges says a restart
	// is needed; this is how the owner performs it without leaving the page
	// they wrote it from.
	Restarter commands.Restarter
	// Getenv resolves the *_env variables a config names when the raw editor
	// validates a replacement. It defaults to os.Getenv: unlike safe mode,
	// which is handed one because it never loaded a config, the running
	// daemon already holds the environment its current config came from.
	Getenv func(string) string
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

// GoogleConnection is the shared grant as the panel shows it: never a token,
// only whose account it is and whose it should be.
type GoogleConnection struct {
	Authorized    bool
	Email         string
	ExpectedEmail string
}

// GoogleConnectionReader is bootstrap's handoff of the live connection state.
type GoogleConnectionReader interface {
	Connection() (GoogleConnection, error)
}

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

// What /api/mode says about the way in: the username/password form, or
// nothing -- safe mode without a database or identity to check against.
const (
	loginPassword    = "password"
	loginUnavailable = "unavailable"
)

func loginKind(webConfig WebUIConfig) string {
	if loginAvailable(webConfig) {
		return loginPassword
	}
	return loginUnavailable
}

// writeMode answers the unauthenticated probe the UI makes before anything
// else. It carries the theme as well as the mode because this is the only
// response that arrives before first paint: serving the theme any later means
// the panel renders light and then flips to charcoal, and serving it behind
// the session means the login page cannot honour it at all. A theme name is
// not a secret, so there is nothing here for an anonymous caller to learn.
//
// Login says whether a way in exists -- the password form, or nothing in a
// safe mode that cannot identify anyone -- so the login page renders the
// right screen before anyone has a session. That is not a secret: the login
// route answers the same question to anyone who posts to it.
func writeMode(mode string, theme func() string, login string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		name := config.ThemeDark
		if theme != nil {
			name = theme()
		}
		body, err := json.Marshal(struct {
			Mode  string `json:"mode"`
			Theme string `json:"theme"`
			Login string `json:"login"`
		}{Mode: mode, Theme: name, Login: login})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not encode mode")
			return
		}
		_, _ = w.Write(body)
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
	throttle := session.NewLoginThrottle(now)
	limiter := newVerifyLimiter()
	dummy := dummyPasswordHash()
	mux := http.NewServeMux()
	// Every route below the login endpoints is owner-only, so the session
	// check is bound once here rather than repeated on each registration. It
	// reads as part of the route table that way: an unguarded route is one
	// that visibly does not say guard, instead of one that happens to be
	// missing two arguments among thirty that carry them.
	guard := func(next http.HandlerFunc) http.Handler {
		return requireAccountSession(webConfig, now, next)
	}
	mux.Handle("GET /", webUIHandler())
	mux.HandleFunc("GET /api/mode", writeMode(modeNormal, configuredTheme(configPath), loginKind(webConfig)))
	mux.HandleFunc("POST /api/login", handlePasswordLogin(configPath, webConfig, throttle, limiter, dummy, now))
	mux.HandleFunc("POST /api/login/link", handleWebLoginLinkRedeem(configPath, webConfig, throttle, now))
	// Any other method on the credential routes is 405, not the asset
	// server's 404: a GET with a password in the query string must not be
	// mistaken for a missing page.
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		mux.HandleFunc(method+" /api/login", methodNotAllowed)
		mux.HandleFunc(method+" /api/login/link", methodNotAllowed)
	}
	mux.Handle("POST /api/logout", guard(handleAccountLogout(webConfig)))
	mux.Handle("GET /api/session", guard(handleAccountSession))

	for _, section := range []string{"providers", "models", "google", "heartbeat", "tracing", "appearance"} {
		mux.Handle("GET /api/config/"+section, guard(webConfigGetRoute(configPath, section, webConfig)))
		mux.Handle("POST /api/config/"+section, guard(webConfigSetRoute(configPath, section, webConfig)))
	}

	mux.Handle("GET /api/config/models/available", guard(newModelDiscoveryHandler(webConfig.ModelDiscovery)))
	mux.Handle("DELETE /api/config/models/{alias}", guard(webModelRemoveRoute(configPath)))

	// The accounts card. Every write is a config mutation; the routes add
	// only who-may-do-what and the live enrollment/session state.
	mux.Handle("GET /api/config/accounts", guard(accountsGetRoute(configPath, webConfig, now)))
	mux.Handle("POST /api/config/accounts", guard(accountAddRoute(configPath, webConfig, limiter)))
	mux.Handle("POST /api/config/accounts/convert", guard(accountsConvertRoute(configPath, webConfig)))
	mux.Handle("PATCH /api/config/accounts/{id}", guard(accountEditRoute(configPath)))
	mux.Handle("DELETE /api/config/accounts/{id}", guard(accountRemoveRoute(configPath, webConfig)))
	mux.Handle("POST /api/config/accounts/{id}/password", guard(accountPasswordRoute(configPath, webConfig, limiter)))
	mux.Handle("POST /api/config/accounts/{id}/revoke-sessions", guard(accountRevokeSessionsRoute(configPath, webConfig)))
	mux.Handle("POST /api/config/google/expected-email", guard(expectedEmailSetRoute(configPath)))
	mux.Handle("POST /api/config/telegram/enabled", guard(telegramEnabledRoute(configPath, webConfig)))
	if webConfig.IdentityLinks != nil && webConfig.TelegramBotUsername != "" {
		mux.Handle("POST /api/accounts/{id}/telegram/pairing", guard(telegramPairingCreateRoute(webConfig.IdentityLinks, webConfig.TelegramBotUsername, cryptoRead, now)))
	}
	mux.Handle("DELETE /api/accounts/{id}/telegram", guard(telegramUnlinkRoute(configPath, webConfig.IdentityLinks)))
	if webConfig.IdentityLinks != nil && webConfig.DiscordLinking {
		mux.Handle("POST /api/accounts/{id}/discord/link", guard(discordLinkCreateRoute(webConfig.IdentityLinks, webConfig.DiscordApplicationID, cryptoRead, now)))
	}
	mux.Handle("DELETE /api/accounts/{id}/discord", guard(discordUnlinkRoute(configPath, webConfig.IdentityLinks)))
	// The Discord card: config plus the one credential set from the panel.
	mux.Handle("GET /api/config/discord", guard(discordGetRoute(configPath, webConfig)))
	mux.Handle("POST /api/config/discord", guard(discordSetRoute(configPath, webConfig)))
	mux.Handle("DELETE /api/config/discord/token", guard(discordTokenClearRoute(webConfig)))

	mux.Handle("GET /api/config/raw", guard(rawConfigGetRoute(configPath)))
	mux.Handle("POST /api/config/raw", guard(rawConfigSetRoute(configPath, webConfig.Getenv, nil)))

	mux.Handle("GET /api/config/mcp", guard(webMCPListRoute(configPath)))
	mux.Handle("POST /api/config/mcp", guard(webMCPSetRoute(configPath)))
	mux.Handle("DELETE /api/config/mcp/{name}", guard(webMCPRemoveRoute(configPath)))
	// Starting an OAuth flow is owner-only: an anonymous visitor who could
	// reach this would bind their own account as Eggy's credential for that
	// server. The matching callback is deliberately not session-gated -- it is
	// the provider's redirect, authenticated by the state parameter it carries.
	if webConfig.MCP != nil {
		mux.Handle("GET /auth/mcp/{server}", guard(webMCPLoginRoute(webConfig.MCP)))
	}

	mux.Handle("GET /api/chat/threads", guard(newThreadListHandler(webConfig.Threads)))
	mux.Handle("POST /api/chat/threads", guard(newThreadCreateHandler(webConfig.Threads, now)))
	mux.Handle("PATCH /api/chat/threads/{id}", guard(newThreadRenameHandler(webConfig.Threads)))
	mux.Handle("DELETE /api/chat/threads/{id}", guard(newThreadDeleteHandler(webConfig.Threads)))
	mux.Handle("GET /api/chat/threads/{id}/history", guard(newThreadHistoryHandler(webConfig.Threads, webConfig.Memory)))
	mux.Handle("GET /api/chat/threads/{id}/stream", guard(newThreadStreamHandler(webConfig.ChatHub, webConfig.Threads, sessionRevalidator(webConfig, now))))
	mux.Handle("POST /api/chat/threads/{id}/send", guard(newThreadSendHandler(webConfig.Enqueue, webConfig.Threads)))
	mux.Handle("GET /api/tools", guard(newToolListHandler(webConfig.Tools)))
	mux.Handle("GET /api/approvals", guard(newApprovalListHandler(webConfig.Approvals, now)))
	mux.Handle("GET /api/approvals/mode", guard(newApprovalModeHandler(webConfig.ApprovalMode, false)))
	mux.Handle("POST /api/approvals/mode", guard(newApprovalModeHandler(webConfig.ApprovalMode, true)))
	mux.Handle("GET /api/agent", guard(newAgentHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/model", guard(newAgentModelHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/effort", guard(newAgentEffortHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/thinking", guard(newAgentThinkingHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/heartbeat", guard(newAgentHeartbeatHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("GET /api/context/watch", guard(newWatchGetRoute(webConfig.Documents)))
	mux.Handle("POST /api/context/watch", guard(newWatchSetRoute(webConfig.Documents)))
	mux.Handle("GET /api/context/soul", guard(newSoulGetRoute(webConfig.Documents)))
	mux.Handle("POST /api/context/soul", guard(newSoulSetRoute(webConfig.Documents)))
	if webConfig.Traces != nil {
		mux.Handle("GET /api/traces", guard(newTraceListHandler(webConfig.Traces)))
		mux.Handle("GET /api/traces/{id}", guard(newTraceDetailHandler(webConfig.Traces)))
	}
	mux.Handle("GET /api/schedules", guard(newScheduleListHandler(webConfig.Schedules)))
	mux.Handle("DELETE /api/schedules/{id}", guard(newScheduleDeleteHandler(webConfig.Schedules)))

	mux.Handle("POST /api/restart", guard(newRestartHandler(webConfig.Restarter, configPath, webConfig.Getenv)))

	mux.Handle("POST /api/chat/approve", guard(newChatApproveHandler(webConfig.Enqueue)))

	return mux
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func webUIHandler() http.Handler {
	fileServer := http.FileServer(http.FS(webui.Assets()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && isApplicationRoute(r.URL.Path) {
			if r.URL.Path == "/auth/link" {
				// The login token rides in this page's fragment. The
				// fragment never reaches the server, but the page must not
				// be cached with it in a history entry, nor leak its URL
				// as a referrer to anything the shell loads.
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			request := r.Clone(r.Context())
			request.URL.Path = "/"
			fileServer.ServeHTTP(w, request)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func isApplicationRoute(path string) bool {
	return path == "/settings" || path == "/settings/" || path == "/traces" || path == "/traces/" || path == "/auth/link"
}

func webModelRemoveRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alias := r.PathValue("alias")
		if err := config.RemoveModelAlias(configPath, alias); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Removed model " + alias + ".", Detail: restartToApply})
	}
}

// splitList reads a comma-separated form field into a list. The web form
// carries flat strings, so a multi-valued field arrives as one of these rather
// than as JSON; empty entries are dropped so a trailing comma is not a product
// named "".

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
