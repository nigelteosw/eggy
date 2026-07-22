package bootstrap

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/nigelteosw/eggy/internal/adapters/webui"
)

// WebUIConfig holds what NewWebHandler needs beyond the config file path:
// the single owner login credential and the key used to sign session
// cookies (Eggy's existing EGGY_ENCRYPTION_KEY -- see the design spec at
// docs/superpowers/specs/2026-07-22-web-config-ui-design.md).
type WebUIConfig struct {
	UserEmail  string
	Password   string
	SigningKey []byte
	Now        func() time.Time
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

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(webui.Assets())))
	mux.HandleFunc("POST /api/login", handleWebLogin(webConfig, throttle, now))
	mux.HandleFunc("POST /api/logout", handleWebLogout())
	mux.Handle("GET /api/session", requireWebSession(webConfig, now, func(w http.ResponseWriter, _ *http.Request) {
		writeWebResult(w, CommandResult{State: ResultSuccess, Title: "Session is valid."})
	}))
	return mux
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
