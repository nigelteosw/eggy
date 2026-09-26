// Local account login: username and password against the SQLite credential
// record, or the operator's environment credentials for the one account
// web.password_account_id binds. Both end in the same account session; there
// is no second cookie, no second guard, and no second way in.
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/auth/session"
	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// authBodyLimit bounds every credential-bearing request body. A username
// and a 256-byte password fit in a fraction of it; anything larger is not a
// login.
const authBodyLimit = 4 << 10

// verificationSlots bounds concurrent password verifications. PBKDF2 at the
// configured work factor costs tens of milliseconds of CPU per attempt; two
// at a time is enough for two people signing in and too few for a flood of
// wrong guesses to become a CPU sink. A request that finds both taken is
// refused with 429 rather than queued, for the same reason the throttle
// refuses instead of sleeping.
const verificationSlots = 2

type verifyLimiter chan struct{}

func newVerifyLimiter() verifyLimiter { return make(verifyLimiter, verificationSlots) }

func (l verifyLimiter) acquire() bool {
	select {
	case l <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l verifyLimiter) release() { <-l }

// decodeAuthBody reads a bounded, strict JSON body. Unknown fields and
// trailing data are errors, so a request cannot smuggle a second credential
// under a name nobody checks.
func decodeAuthBody(r *http.Request, into any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, authBodyLimit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing data")
	}
	return nil
}

// membership is what the current config says about one account at the
// moment a credential operation commits: the record, who the environment
// credentials belong to, and whether Telegram is a channel.
type membership struct {
	account           AccountRecord
	passwordAccountID string
	telegramEnabled   bool
}

// withMembership resolves id against the config as it is now, under the
// config lock, and runs fn while it is held. With no config path (tests of
// the login routes alone) the configured directory answers instead. This is
// the one membership check every session-creating and credential-changing
// path shares; the directory the guard uses is the same authority read
// without the lock.
func withMembership(configPath string, webConfig WebUIConfig, id string, fn func(membership) error) error {
	if configPath == "" {
		account, ok := webConfig.Accounts.Account(id)
		if !ok {
			return errors.New("account is not configured")
		}
		return fn(membership{account: account, passwordAccountID: webConfig.Accounts.PasswordAccountID(), telegramEnabled: webConfig.Accounts.TelegramEnabled()})
	}
	return config.WithAccount(configPath, id, func(cfg config.Config, account config.AccountConfig) error {
		return fn(membership{
			account:           AccountRecord{ID: account.ID, TelegramUserID: account.TelegramUserID, DiscordUserID: account.DiscordUserID},
			passwordAccountID: cfg.PasswordAccountID(),
			telegramEnabled:   cfg.TelegramEnabled(),
		})
	})
}

// loginAvailable reports whether the password route has everything it needs.
func loginAvailable(webConfig WebUIConfig) bool {
	return webConfig.Auth != nil && webConfig.Sessions != nil && webConfig.Accounts != nil
}

// environmentBound reports whether id is the account the environment
// credentials sign in, as configured when this process started.
func environmentBound(webConfig WebUIConfig, id string) bool {
	return webConfig.PasswordAccountID != "" && id == webConfig.PasswordAccountID
}

// handlePasswordLogin is POST /api/login. The order matters: the throttle
// and body bound come before any lookup, the password is verified outside
// every lock, and the session is created only after membership is rechecked
// under the config lock against the generation the password was verified
// for. An unknown username, a pending account, and a malformed stored hash
// all verify a dummy hash so their failure costs what a wrong password does.
func handlePasswordLogin(configPath string, webConfig WebUIConfig, throttle *session.LoginThrottle, limiter verifyLimiter, dummyHash string, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, webConfig.TrustedProxyHops)
		if delay := throttle.Delay(ip); delay > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((delay+time.Second-1)/time.Second)))
			writeWebError(w, http.StatusTooManyRequests, "too many failed login attempts, try again shortly")
			return
		}
		var credentials struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeAuthBody(r, &credentials); err != nil {
			writeWebError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		username := strings.TrimSpace(credentials.Username)
		if username == "" || credentials.Password == "" || len(credentials.Password) > session.MaxPasswordBytes {
			writeWebError(w, http.StatusBadRequest, "username and password are required")
			return
		}
		if !loginAvailable(webConfig) {
			writeWebError(w, http.StatusUnauthorized, "web login is not available")
			return
		}
		deny := func() {
			throttle.RecordFailure(ip)
			writeWebError(w, http.StatusUnauthorized, "invalid username or password")
		}
		// Resolve the identity server-side: the exact ID, or the environment
		// alias standing for the bound account. Nothing typed selects a
		// hash the caller did not earn.
		id, known := resolveUsername(webConfig, username)
		var record ports.AccountAuth
		if known {
			var err error
			record, err = webConfig.Auth.AccountAuth(r.Context(), id)
			switch {
			case errors.Is(err, ports.ErrAuthDenied):
				known = false
			case err != nil:
				writeWebError(w, http.StatusServiceUnavailable, "login is temporarily unavailable")
				return
			case record.Retired:
				known = false
			}
		}
		encoded := dummyHash
		if known {
			if environmentBound(webConfig, id) {
				encoded = webConfig.EnvironmentPasswordHash
			} else if record.PasswordHash != "" {
				encoded = record.PasswordHash
			} else {
				known = false
			}
		}
		if !limiter.acquire() {
			w.Header().Set("Retry-After", "1")
			writeWebError(w, http.StatusTooManyRequests, "too many logins in progress, try again shortly")
			return
		}
		verified := session.VerifyPassword(encoded, credentials.Password)
		limiter.release()
		if !known || !verified {
			deny()
			return
		}
		raw, hash, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not create a session")
			return
		}
		expiresAt := now().Add(webSessionTTL)
		err = withMembership(configPath, webConfig, id, func(m membership) error {
			// The environment password belongs to whoever the config bound
			// when this process read it. A binding moved since then is
			// refused until restart rather than handing a boot-time secret
			// to a newly selected person.
			if environmentBound(webConfig, id) != (m.passwordAccountID == id) {
				return ports.ErrAuthDenied
			}
			return webConfig.Auth.CreateAuthenticatedSession(r.Context(), hash, id, record.Generation, expiresAt)
		})
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrAuthDenied):
			deny()
			return
		default:
			// Membership failed to resolve or the store refused: the
			// password was right, so this is not a throttle failure, and no
			// cookie may leave.
			writeWebError(w, http.StatusServiceUnavailable, "login is temporarily unavailable")
			return
		}
		throttle.Reset(ip)
		setAccountSessionCookie(w, raw, expiresAt)
		writeWebResult(w, webResult{State: webSuccess, Title: "Logged in."})
	}
}

// resolveUsername maps the typed name to an account ID: an exact configured
// ID, or the environment alias, compared case-insensitively, standing for
// the bound account.
func resolveUsername(webConfig WebUIConfig, username string) (string, bool) {
	if alias := strings.TrimSpace(webConfig.EnvironmentAlias); alias != "" && strings.EqualFold(username, alias) {
		if webConfig.PasswordAccountID == "" {
			return "", false
		}
		if _, ok := webConfig.Accounts.Account(webConfig.PasswordAccountID); !ok {
			return "", false
		}
		return webConfig.PasswordAccountID, true
	}
	account, ok := webConfig.Accounts.Account(username)
	if !ok {
		return "", false
	}
	return account.ID, true
}

// dummyPasswordHash is one valid encoding made at handler construction, so
// a login for a username that does not exist costs the same verification a
// wrong password does. Made once: hashing per request would be the cost
// without the point.
func dummyPasswordHash() string {
	encoded, err := session.HashPassword("eggy-dummy-verification-password")
	if err != nil {
		// rand.Read failing is the only way here; verification against an
		// empty encoding is simply false, which keeps the route safe if
		// slightly cheaper to probe.
		return ""
	}
	return encoded
}

// accountAuthPresent is the session guard's second check: the account's
// credential row must exist and not be retired. It is what stops an ID
// pasted back into YAML from resuming the removed person's sessions.
func accountAuthPresent(ctx context.Context, webConfig WebUIConfig, id string) bool {
	if webConfig.Auth == nil {
		return false
	}
	record, err := webConfig.Auth.AccountAuth(ctx, id)
	return err == nil && !record.Retired
}
