package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/internal/commands"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/kernel/events"
	"github.com/nigelteosw/eggy/plugins/auth/session"
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
	// ModelDiscovery browses a provider's catalog so the models card can offer
	// what is on sale instead of asking the owner to type an ID from memory.
	// Nil leaves the route answering 404 and the card's browse control absent,
	// which is also what every provider opting out of discovery produces.
	ModelDiscovery ModelDiscoverer
	// Watch is the heartbeat's watch list, the one context document the
	// panel edits. Nil leaves its routes answering 404 and the card absent.
	Watch WatchList
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
	// webLoginLinkTTL bounds how long a /web link is worth stealing. It is
	// minutes rather than hours because the link travels through a chat
	// transcript, which is a place credentials linger.
	webLoginLinkTTL = 5 * time.Minute
)

// spentLinks remembers the login links already exchanged for a session, so a
// link left behind in a chat transcript cannot be replayed by whoever reads it
// later. Entries are dropped once their token could no longer verify anyway,
// which bounds the map by the link TTL rather than by uptime.
type spentLinks struct {
	mu    sync.Mutex
	spent map[string]time.Time
}

func newSpentLinks() *spentLinks {
	return &spentLinks{spent: make(map[string]time.Time)}
}

// claim records token as spent and reports whether this caller is the one that
// spent it. A second call with the same token returns false.
func (s *spentLinks) claim(token string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for spent, at := range s.spent {
		if now.Sub(at) > webLoginLinkTTL {
			delete(s.spent, spent)
		}
	}
	if _, used := s.spent[token]; used {
		return false
	}
	s.spent[token] = now
	return true
}

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

// writeMode answers the unauthenticated probe the UI makes before anything
// else. It carries the theme as well as the mode because this is the only
// response that arrives before first paint: serving the theme any later means
// the panel renders light and then flips to charcoal, and serving it behind
// the session means the login page cannot honour it at all. A theme name is
// not a secret, so there is nothing here for an anonymous caller to learn.
func writeMode(mode string, theme func() string) http.HandlerFunc {
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
		}{Mode: mode, Theme: name})
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not encode mode")
			return
		}
		_, _ = w.Write(body)
	}
}

// rawConfigGetRoute hands back config.yaml as the owner wrote it, comments and
// all. Shared by safe mode and the running panel: safe mode is where it began,
// but reaching it only by breaking Eggy first meant the file was uneditable
// from a browser in every case except the emergency.
func rawConfigGetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		body, err := config.ReadConfigText(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
}

// rawConfigSetRoute replaces config.yaml, but only with a body LoadConfig has
// already accepted, so neither surface can write a config that would not
// start. repaired may be nil: safe mode uses it to hand control back to the
// supervisor, while the running daemon has nothing to retry -- a live process
// picks the new file up on its next restart, which is the owner's call and
// which /restart in chat performs.
func rawConfigSetRoute(configPath string, getenv func(string) string, repaired func()) http.HandlerFunc {
	if getenv == nil {
		getenv = os.Getenv
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// The file is small by construction and the writer is the
		// authenticated owner, but the cap keeps a stuck or hostile client
		// from filling the volume.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeWebError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		if err := config.ReplaceConfig(configPath, body, getenv); err != nil {
			// A rejected config is never written: the owner still has the file
			// they had, plus the reason this one would not have started.
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		if repaired != nil {
			repaired()
			writeWebResult(w, webResult{State: webSuccess, Title: "Config saved.", Detail: "Eggy is starting up again. Reload in a few seconds."})
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Config saved.", Detail: restartToApply})
	}
}

// configuredTheme reads the theme for the probe above. A config it cannot
// read falls back to the default rather than failing the probe: the mode is
// the load-bearing half of that response, and refusing to report it because a
// cosmetic preference was unreadable would blank the whole panel.
func configuredTheme(configPath string) func() string {
	return func() string {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			return config.ThemeDark
		}
		return cfg.Appearance.ResolvedTheme()
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
	links := newSpentLinks()
	mux := http.NewServeMux()
	// Every route below the login endpoints is owner-only, so the session
	// check is bound once here rather than repeated on each registration. It
	// reads as part of the route table that way: an unguarded route is one
	// that visibly does not say guard, instead of one that happens to be
	// missing two arguments among thirty that carry them.
	guard := func(next http.HandlerFunc) http.Handler {
		return requireWebSession(webConfig, now, next)
	}
	mux.Handle("GET /", webUIHandler())
	mux.HandleFunc("GET /api/mode", writeMode(modeNormal, configuredTheme(configPath)))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.HandleFunc("GET /auth/link", handleWebLoginLink(webConfig, links, now))
	mux.Handle("GET /api/session", guard(func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, webResult{State: webSuccess, Title: "Session is valid."})
	}))

	for _, section := range []string{"providers", "models", "google", "heartbeat", "tracing", "appearance"} {
		mux.Handle("GET /api/config/"+section, guard(webConfigGetRoute(configPath, section, webConfig)))
		mux.Handle("POST /api/config/"+section, guard(webConfigSetRoute(configPath, section, webConfig)))
	}

	mux.Handle("GET /api/config/models/available", guard(newModelDiscoveryHandler(webConfig.ModelDiscovery)))

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
	mux.Handle("GET /api/chat/threads/{id}/stream", guard(newThreadStreamHandler(webConfig.ChatHub, webConfig.Threads)))
	mux.Handle("POST /api/chat/threads/{id}/send", guard(newThreadSendHandler(webConfig.Enqueue, webConfig.OwnerID, webConfig.Threads)))
	mux.Handle("GET /api/tools", guard(newToolListHandler(webConfig.Tools)))
	mux.Handle("GET /api/approvals", guard(newApprovalListHandler(webConfig.Approvals, now)))
	mux.Handle("GET /api/approvals/mode", guard(newApprovalModeHandler(webConfig.ApprovalMode, false)))
	mux.Handle("POST /api/approvals/mode", guard(newApprovalModeHandler(webConfig.ApprovalMode, true)))
	mux.Handle("GET /api/agent", guard(newAgentHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/model", guard(newAgentModelHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("POST /api/agent/effort", guard(newAgentEffortHandler(webConfig.Agent, webConfig.ApprovalMode)))
	mux.Handle("GET /api/context/watch", guard(newWatchGetRoute(webConfig.Watch)))
	mux.Handle("POST /api/context/watch", guard(newWatchSetRoute(webConfig.Watch)))
	if webConfig.Traces != nil {
		mux.Handle("GET /api/traces", guard(newTraceListHandler(webConfig.Traces)))
		mux.Handle("GET /api/traces/{id}", guard(newTraceDetailHandler(webConfig.Traces)))
	}
	mux.Handle("GET /api/schedules", guard(newScheduleListHandler(webConfig.Schedules)))
	mux.Handle("DELETE /api/schedules/{id}", guard(newScheduleDeleteHandler(webConfig.Schedules)))

	mux.Handle("POST /api/restart", guard(newRestartHandler(webConfig.Restarter, configPath, webConfig.Getenv)))

	mux.Handle("POST /api/chat/approve", guard(newChatApproveHandler(webConfig.Enqueue, webConfig.OwnerID)))

	return mux
}

func webUIHandler() http.Handler {
	fileServer := http.FileServer(http.FS(webui.Assets()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && isApplicationRoute(r.URL.Path) {
			request := r.Clone(r.Context())
			request.URL.Path = "/"
			fileServer.ServeHTTP(w, request)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func isApplicationRoute(path string) bool {
	return path == "/settings" || path == "/settings/" || path == "/traces" || path == "/traces/"
}

func webMCPListRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		servers, err := config.GetMCPServersConfig(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		names := slices.Sorted(maps.Keys(servers))
		rows := make([][]string, 0, len(names))
		for _, name := range names {
			server := servers[name]
			// Only names are ever rendered, never values: bearer_token_env and
			// oauth_client_secret_env are variable names, and the client id is
			// public by construction. No secret reaches this response.
			rows = append(rows, []string{name, server.Transport, server.URL, server.Auth, strconv.FormatBool(server.Enabled), server.BearerTokenEnv, server.OAuthClientID, server.OAuthClientSecretEnv})
		}
		writeWebResult(w, webResult{
			State:        webSuccess,
			TableHeaders: []string{"Name", "Transport", "URL", "Auth", "Enabled", "Bearer token env", "OAuth client id", "OAuth client secret env"},
			TableRows:    rows,
		})
	}
}

func webMCPSetRoute(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var values config.Values
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		name := values["name"]
		if name == "" || values["url"] == "" || values["auth"] == "" {
			writeWebError(w, http.StatusBadRequest, "name, url, and auth are required")
			return
		}
		input, decodeErr := values.MCPServerInput(name)
		if decodeErr != nil {
			writeWebError(w, http.StatusBadRequest, decodeErr.Error())
			return
		}
		if err := config.SetMCPServer(configPath, input); err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeWebResult(w, webResult{State: webSuccess, Title: "Saved MCP server " + name + ".", Detail: restartToApply})
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
		writeWebResult(w, webResult{State: webSuccess, Title: "Removed MCP server " + name + ".", Detail: restartToApply})
	}
}

func webConfigGetRoute(configPath, section string, webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.LoadDocument(configPath)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result := webResult{State: webSuccess}
		switch section {
		case "providers":
			names := slices.Sorted(maps.Keys(cfg.Providers))
			result.TableHeaders = []string{"Provider", "Adapter", "Base URL", "API key env", "Discover models"}
			for _, name := range names {
				provider := cfg.Providers[name]
				discover := "no"
				if provider.DiscoversModels() {
					discover = "yes"
				}
				result.TableRows = append(result.TableRows, []string{name, provider.Adapter, provider.BaseURL, provider.APIKeyEnv, discover})
			}
		case "models":
			aliases := slices.Sorted(maps.Keys(cfg.ModelAliases))
			result.TableHeaders = []string{"Alias", "Provider", "Model", "Reasoning efforts"}
			for _, alias := range aliases {
				model := cfg.ModelAliases[alias]
				result.TableRows = append(result.TableRows, []string{alias, model.Provider, model.Model, strings.Join(model.ReasoningEfforts, ", ")})
			}
			// Which providers can be browsed rides along with the section the
			// card already fetches, rather than costing a second round trip to
			// answer a question that only changes when config does.
			if webConfig.ModelDiscovery != nil {
				result.Lines = webConfig.ModelDiscovery.DiscoverableProviders()
			}
		case "google":
			// One row, because there is one grant. A second row would suggest
			// per-product configuration that does not exist.
			state := "disabled"
			if cfg.Google.Enabled {
				state = "enabled"
			}
			result.TableHeaders = []string{"State", "Client ID", "Client secret env", "Products"}
			result.TableRows = append(result.TableRows, []string{state, cfg.Google.ClientID, cfg.Google.ClientSecretEnv, strings.Join(cfg.Google.Products, ", ")})
			result.Fields = googleApprovalFields(cfg, webConfig.GoogleActions)
		case "heartbeat":
			// One row, like Google: there is one heartbeat. A zero interval
			// is reported as "off" rather than as "0s", because off is the
			// state the owner is looking for and a duration that means off
			// reads like a misconfiguration.
			interval := "off"
			if cfg.Heartbeat.Interval.Value() > 0 {
				interval = cfg.Heartbeat.Interval.Value().String()
			}
			instruction := cfg.Heartbeat.Instruction
			if strings.TrimSpace(instruction) == "" {
				instruction = "(built-in default)"
			}
			// The window and the history relaxation are reported even though
			// only the window is settable here: a setting the panel hides is
			// one the owner cannot discover is on.
			window := "any hour"
			if hours := cfg.Heartbeat.ActiveHours; hours.Configured() {
				window = hours.Start + "-" + hours.End
			}
			history := "isolated"
			if cfg.Heartbeat.IncludeRecentHistory {
				history = "recent history (config.yaml only)"
			}
			result.TableHeaders = []string{"Interval", "Instruction", "Active hours", "Context"}
			result.TableRows = append(result.TableRows, []string{interval, instruction, window, history})
		case "tracing":
			// One row: there is one trace recorder. Off is reported as off
			// rather than as a set of limits that never apply, because that
			// is the state the owner is looking for.
			state := "on"
			if !cfg.Tracing.Active() {
				state = "off"
			}
			result.TableHeaders = []string{"Tracing", "Turns kept", "Kept for", "Max body"}
			result.TableRows = append(result.TableRows, []string{
				state,
				strconv.Itoa(cfg.Tracing.KeepTurns),
				cfg.Tracing.Retention.Value().String(),
				strconv.FormatInt(cfg.Tracing.MaxBodyBytes, 10),
			})
		case "appearance":
			result.Fields = []webField{{Label: "theme", Value: cfg.Appearance.ResolvedTheme()}}
		}
		writeWebResult(w, result)
	}
}

// GoogleProductActions is one product's surface as the panel needs it: every
// action it accepts, and the subset that writes. The second is what the form
// pre-selects, so an owner opening the card sees the default they already have
// rather than an empty grid that would gate nothing if they saved it.
type GoogleProductActions struct {
	Actions   []string
	Mutations []string
}

// googleApprovalFields describes the gate to the form.
//
// require_approval_mode distinguishes the two states that look alike and are
// not: "default" means no key is stored and each tool's own classification
// decides -- including actions added by a later version -- while "custom"
// means the stored list is the whole of it, and an empty custom list gates
// nothing at all.
func googleApprovalFields(cfg config.Config, catalog map[string]GoogleProductActions) []webField {
	mode, stored := "default", []string(nil)
	if cfg.Google.RequireApproval != nil {
		mode, stored = "custom", *cfg.Google.RequireApproval
	}
	fields := []webField{
		{Label: "require_approval_mode", Value: mode},
		{Label: "require_approval", Value: strings.Join(stored, ",")},
	}
	for _, product := range slices.Sorted(maps.Keys(catalog)) {
		fields = append(fields,
			webField{Label: "actions." + product, Value: strings.Join(catalog[product].Actions, ",")},
			webField{Label: "mutations." + product, Value: strings.Join(catalog[product].Mutations, ",")},
		)
	}
	return fields
}

// checkGoogleApprovals refuses an entry the adapter would refuse at startup,
// while the existing config is still in place. The panel builds its checkboxes
// from the same catalog, so this is unreachable through the form itself -- it
// is here for anything else that can POST.
func checkGoogleApprovals(entries []string, catalog map[string]GoogleProductActions) error {
	for _, entry := range entries {
		product, action, qualified := strings.Cut(entry, ".")
		known, exists := catalog[product]
		if !exists {
			return fmt.Errorf("%q names no Google product", entry)
		}
		if !qualified {
			return fmt.Errorf("%q must name an action, as in %q, or %q for all of them", entry, product+".<action>", product+".*")
		}
		if action != "*" && !slices.Contains(known.Actions, action) {
			return fmt.Errorf("%q: %s has no action %q", entry, product, action)
		}
	}
	return nil
}

// splitList reads a comma-separated form field into a list. The web form
// carries flat strings, so a multi-valued field arrives as one of these rather
// than as JSON; empty entries are dropped so a trailing comma is not a product
// named "".

func webConfigSetRoute(configPath, section string, webConfig WebUIConfig) http.HandlerFunc {
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
			input := config.ProviderInput{
				Name: named["name"], Adapter: named["adapter"],
				BaseURL: named["base_url"], APIKeyEnv: named["api_key_env"],
			}
			// Absent leaves the key out and takes the default; only a form that
			// actually carried the control writes a choice down.
			if sent, ok := named["discover_models"]; ok {
				discover := sent != "false"
				input.DiscoverModels = &discover
			}
			err = config.SetProvider(configPath, input)
			title = "Set provider " + named["name"] + "."
		case "models":
			err = config.SetModelAlias(configPath, named["alias"], named["provider"], named["model"], named["reasoning_efforts"])
			title = "Set model " + named["alias"] + "."
		case "google":
			// Decoded by internal/config, not mapped field by field here: the
			// chat surface decodes the same keys through the same function, so
			// neither surface can grow a field the other does not know about.
			input, decodeErr := config.Values(named).GoogleInput()
			if decodeErr != nil {
				writeWebError(w, http.StatusBadRequest, decodeErr.Error())
				return
			}
			// Three states through one pointer, the same distinction the
			// setting itself turns on: absent leaves the stored list alone, a
			// pointer to nil restores the default by removing the key, and a
			// pointer to a list -- empty included -- replaces it.
			if mode, sent := named["require_approval_mode"]; sent {
				entries := config.SplitCommaList(named["require_approval"])
				if mode == "default" {
					entries = nil
				} else if entries == nil {
					entries = []string{}
				}
				if err := checkGoogleApprovals(entries, webConfig.GoogleActions); err != nil {
					writeWebError(w, http.StatusBadRequest, err.Error())
					return
				}
				input.RequireApproval = &entries
			}
			err = config.SetGoogle(configPath, input)
			title = "Saved Google Workspace."
		case "heartbeat":
			err = config.SetHeartbeat(configPath, named["interval"], named["instruction"], named["active_start"], named["active_end"])
			title = "Saved heartbeat."
			if strings.TrimSpace(named["interval"]) == "" {
				title = "Heartbeat turned off."
			}
		case "tracing":
			err = config.SetTracing(configPath, named["enabled"], named["keep_turns"], named["retention"], named["max_body_bytes"])
			title = "Saved tracing."
			if named["enabled"] == "false" {
				title = "Tracing turned off."
			}
		case "appearance":
			err = config.SetAppearance(configPath, named["theme"])
			title = "Saved appearance."
		}
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Appearance is the one section a restart does not gate: nothing in the
		// running process reads it, so the page the owner is looking at has
		// already applied it by the time this response lands.
		detail := restartToApply
		if section == "appearance" {
			detail = ""
		}
		writeWebResult(w, webResult{State: webSuccess, Title: title, Detail: detail})
	}
}

func handleWebLogin(webConfig WebUIConfig, throttle *session.LoginThrottle, now func() time.Time) http.HandlerFunc {
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
			Name: webSessionCookie, Value: session.SignSession(webConfig.SigningKey, expiresAt),
			Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt,
		})
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged in."})
	}
}

// handleWebLoginLink spends a token minted by /web and lands the owner in the
// panel already signed in. It is the same authority as a password login --
// there is one account -- so it issues the same cookie and nothing more.
func handleWebLoginLink(webConfig WebUIConfig, links *spentLinks, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if len(webConfig.SigningKey) == 0 || !session.VerifyLoginLink(webConfig.SigningKey, token, now()) {
			writeWebError(w, http.StatusUnauthorized, "this sign-in link is invalid or has expired -- send /web again")
			return
		}
		if !links.claim(token, now()) {
			writeWebError(w, http.StatusUnauthorized, "this sign-in link has already been used -- send /web again")
			return
		}
		expiresAt := now().Add(webSessionTTL)
		http.SetCookie(w, &http.Cookie{
			Name: webSessionCookie, Value: session.SignSession(webConfig.SigningKey, expiresAt),
			Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
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
		if err != nil || !session.VerifySession(webConfig.SigningKey, cookie.Value, now()) {
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
