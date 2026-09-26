// Redemption of the single-use browser login links /web mints from a
// verified Telegram chat. The link carries its token in the URL fragment,
// which never reaches this server; the page it lands on posts the token
// here once the person clicks Continue. Nothing is consumed by a GET, a
// preview fetch, or a prefetch.
package panel

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/ports"
)

// loginLinkHeader is the custom header the confirmation page sends. This
// POST has no session to bind a CSRF token to, so the browser boundary is
// the combination of a same-origin Origin, this header (a cross-origin page
// cannot add it without a preflight this server never grants), and the
// explicit click that sent it.
const loginLinkHeader = "X-Eggy-Login"

// webLoginTokenPattern is the shape session.NewToken produces: 32 bytes as
// unpadded URL-safe base64. Anything else is refused before the store is
// asked.
var webLoginTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// handleWebLoginLinkRedeem is POST /api/login/link.
func handleWebLoginLinkRedeem(configPath string, webConfig WebUIConfig, throttle *session.LoginThrottle, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, webConfig.TrustedProxyHops)
		if delay := throttle.Delay(ip); delay > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((delay+time.Second-1)/time.Second)))
			writeWebError(w, http.StatusTooManyRequests, "too many failed login attempts, try again shortly")
			return
		}
		if !loginLinkOrigin(webConfig, r) || r.Header.Get(loginLinkHeader) != "1" {
			writeWebError(w, http.StatusForbidden, "request did not come from the Eggy sign-in page")
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if err := decodeAuthBody(r, &input); err != nil || !webLoginTokenPattern.MatchString(input.Token) {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if !loginAvailable(webConfig) {
			writeWebError(w, http.StatusUnauthorized, "web login is not available")
			return
		}
		deny := func() {
			throttle.RecordFailure(ip)
			writeWebError(w, http.StatusUnauthorized, "this sign-in link is invalid, expired, or already used -- send /web again")
		}
		linkHash := session.HashToken(input.Token)
		link, err := webConfig.Auth.WebLoginLink(r.Context(), linkHash, now())
		switch {
		case errors.Is(err, ports.ErrAuthDenied):
			deny()
			return
		case err != nil:
			writeWebError(w, http.StatusServiceUnavailable, "login is temporarily unavailable")
			return
		}
		raw, sessionHash, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not create a session")
			return
		}
		expiresAt := now().Add(webSessionTTL)
		err = withMembership(configPath, webConfig, link.AccountID, func(m membership) error {
			// The link is only as good as the mapping that minted it: the
			// same sender must still speak for the same account, Telegram
			// must still be on, and the credential generation must not
			// have moved. The store checks generation and sender again
			// inside the transaction; this is the membership half.
			if !m.telegramEnabled || m.account.TelegramUserID == 0 || strconv.FormatInt(m.account.TelegramUserID, 10) != link.SenderID {
				return ports.ErrAuthDenied
			}
			return webConfig.Auth.RedeemWebLoginLink(r.Context(), linkHash, sessionHash, link, now(), expiresAt)
		})
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrAuthDenied):
			deny()
			return
		default:
			// Membership failed to resolve or the transaction rolled back:
			// the link is untouched and no cookie leaves.
			writeWebError(w, http.StatusServiceUnavailable, "login is temporarily unavailable")
			return
		}
		throttle.Reset(ip)
		// Replacing an existing session's cookie is the explicit outcome of
		// the click the page asked for; the previous session itself stays
		// valid for whoever else holds it.
		setAccountSessionCookie(w, raw, expiresAt)
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged in."})
	}
}

// loginLinkOrigin is stricter than sameOrigin: this route is
// unauthenticated, so an absent or null Origin is refused rather than
// treated as a non-browser client.
func loginLinkOrigin(webConfig WebUIConfig, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if public := publicHost(webConfig.PublicBaseURL); public != "" && strings.EqualFold(parsed.Host, public) {
		return true
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
