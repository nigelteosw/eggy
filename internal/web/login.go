// This file is the owner's way in and the gate every other route sits behind:
// the password and one-tap link logins, the session cookie check, and the
// client identity the login throttle keys on.
package web

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/session"
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
