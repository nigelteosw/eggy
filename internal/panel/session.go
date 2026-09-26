// Account sessions: opaque tokens the store knows by hash, revocable one at
// a time or per account, with a CSRF check on every mutating route. Every
// way in -- password, environment credentials, a Telegram /web link -- ends
// in one of these; there is no other cookie.
package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/ports"
)

// SessionStore is what the web layer needs from the database for account
// sessions. Every method deals in hashes; the raw token exists only in the
// browser and in the request that presents it. Sessions are created through
// ports.AccountAuthStore, never here: creation is conditional on a
// credential generation, and this interface has no way to say which.
type SessionStore interface {
	SessionAccount(ctx context.Context, hash string, now time.Time) (string, error)
	RevokeSession(ctx context.Context, hash string) error
	RevokeAccountSessions(ctx context.Context, accountID string) error
	ActiveSessions(ctx context.Context, accountID string, now time.Time) (int, error)
}

// AccountDirectory is the configured account list as the web layer sees it:
// resolve an ID to its record (a removed account resolves to nothing),
// enumerate for the People card, and say which account the environment
// credentials belong to and whether Telegram is a channel, all as the
// config is now.
type AccountDirectory interface {
	Account(id string) (AccountRecord, bool)
	Accounts() []AccountRecord
	PasswordAccountID() string
	TelegramEnabled() bool
}

// AccountRecord is one configured account, provider-neutral.
type AccountRecord struct {
	ID             string
	TelegramUserID int64
	DiscordUserID  string
}

// csrfHeader carries the per-session CSRF token on every mutating request.
// A browser cannot attach a custom header cross-origin without a CORS
// preflight this server never grants, so its presence with the right value
// proves the request came from the panel's own script.
const csrfHeader = "X-Eggy-CSRF"

// csrfToken derives the token for a session from its hash. Derived rather
// than stored: it is only ever compared against the session the cookie
// resolved to, so a second column would record nothing the hash does not.
func csrfToken(sessionHash string) string {
	sum := sha256.Sum256([]byte("csrf:" + sessionHash))
	return hex.EncodeToString(sum[:16])
}

// accountSession is what requireWebSession established for the request.
type accountSession struct {
	hash    string
	account AccountRecord
}

type sessionKey struct{}

func sessionFromContext(ctx context.Context) (accountSession, bool) {
	s, ok := ctx.Value(sessionKey{}).(accountSession)
	return s, ok
}

// resolveAccountSession turns the request's cookie into an account, or
// reports why not. A removed account's sessions are revoked on sight, so a
// person taken off the list is out on their next request, not at expiry.
func resolveAccountSession(webConfig WebUIConfig, r *http.Request, now time.Time) (accountSession, error) {
	cookie, err := r.Cookie(webSessionCookie)
	if err != nil || cookie.Value == "" {
		return accountSession{}, errors.New("not authenticated")
	}
	hash := session.HashToken(cookie.Value)
	accountID, err := webConfig.Sessions.SessionAccount(r.Context(), hash, now)
	if err != nil {
		return accountSession{}, errors.New("not authenticated")
	}
	account, ok := webConfig.Accounts.Account(accountID)
	if !ok || !accountAuthPresent(r.Context(), webConfig, accountID) {
		_ = revokeAccountSessions(r.Context(), webConfig, accountID)
		return accountSession{}, errors.New("not authenticated")
	}
	return accountSession{hash: hash, account: account}, nil
}

// sessionRevalidator is the check an open stream runs on each keepalive: is
// the session that opened it still live? The row is looked up again, which
// is what makes logout, a password reset, and account removal reach an open
// tab.
func sessionRevalidator(webConfig WebUIConfig, now func() time.Time) func(*http.Request) bool {
	return func(r *http.Request) bool {
		_, err := resolveAccountSession(webConfig, r, now())
		return err == nil
	}
}

// revokeAccountSessions ends every session and every open stream of the
// account, in that order: nothing must be delivered after the row is gone.
func revokeAccountSessions(ctx context.Context, webConfig WebUIConfig, accountID string) error {
	if err := webConfig.Sessions.RevokeAccountSessions(ctx, accountID); err != nil {
		return err
	}
	if webConfig.ChatHub != nil {
		webConfig.ChatHub.CloseAccount(accountID)
	}
	return nil
}

// mutating reports whether a request changes something, which is what
// decides whether it must prove it came from the panel.
func mutating(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin refuses a mutating request that says it came from somewhere
// else. Origin is checked when present; Sec-Fetch-Site when present. A
// request carrying neither is a non-browser client (curl, a test), which the
// session cookie and CSRF header already vouch for.
func sameOrigin(webConfig WebUIConfig, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		if !strings.EqualFold(parsed.Host, r.Host) && !strings.EqualFold(parsed.Host, publicHost(webConfig.PublicBaseURL)) {
			return false
		}
	} else if origin == "null" {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

func publicHost(base string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// requireAccountSession is the account-mode guard: a live session, an
// account that still exists, same-origin plus CSRF token on anything that
// writes, and the principal on the context for everything behind it.
func requireAccountSession(webConfig WebUIConfig, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, err := resolveAccountSession(webConfig, r, now())
		if err != nil {
			writeWebError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		if mutating(r) {
			if !sameOrigin(webConfig, r) || !session.Equal(r.Header.Get(csrfHeader), csrfToken(current.hash)) {
				writeWebError(w, http.StatusForbidden, "request did not come from the Eggy panel")
				return
			}
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, current)
		ctx = ports.WithPrincipal(ctx, ports.Principal{AccountID: current.account.ID})
		next(w, r.WithContext(ctx))
	}
}

// setAccountSessionCookie is the one cookie format. Callers set it only
// after the session row has committed: a cookie for a session that failed
// to persist would be a token nothing can resolve, and one set before the
// commit could outlive a rollback.
func setAccountSessionCookie(w http.ResponseWriter, raw string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookie, Value: raw,
		Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// handleAccountLogout revokes the presenting session. It sits behind the
// account guard, so an unauthenticated or cross-site POST cannot log
// someone out; a session that is already gone just clears the cookie.
func handleAccountLogout(webConfig WebUIConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if current, ok := sessionFromContext(r.Context()); ok {
			if err := webConfig.Sessions.RevokeSession(r.Context(), current.hash); err != nil {
				writeWebError(w, http.StatusInternalServerError, "could not end the session")
				return
			}
			// The person's other tabs on other sessions keep streaming; only
			// this session's streams end. Streams are keyed by account, so
			// this closes the account's streams -- the keepalive check
			// reopens nothing for the revoked one and the live sessions'
			// tabs reconnect on their own.
			if webConfig.ChatHub != nil {
				webConfig.ChatHub.CloseAccount(current.account.ID)
			}
		}
		clearSessionCookie(w)
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged out."})
	}
}

// handleAccountSession answers GET /api/session: who is signed in and the
// CSRF token their mutating requests must carry. No token of any other kind
// is in the response.
func handleAccountSession(w http.ResponseWriter, r *http.Request) {
	current, ok := sessionFromContext(r.Context())
	if !ok {
		writeWebError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, map[string]any{
		"state": webSuccess,
		"title": "Session is valid.",
		"account": map[string]any{
			"id":       current.account.ID,
			"username": current.account.ID,
		},
		"csrf": csrfToken(current.hash),
	})
}
