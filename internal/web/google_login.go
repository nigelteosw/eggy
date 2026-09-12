// Google Sign-In: the two routes that turn a verified Google identity into
// an Eggy session. Everything the provider is trusted about happens in
// plugins/auth/google; this file owns the transaction around it -- state,
// nonce, PKCE verifier, browser binding -- and the allowlist decision.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/plugins/auth/google"
	"github.com/nigelteosw/eggy/plugins/auth/session"
)

const (
	// loginCookie binds the callback to the browser that started the login,
	// so a state parameter lifted from one browser cannot complete in
	// another. It lives only as long as the transaction.
	loginCookie    = "eggy_login"
	loginTTL       = 5 * time.Minute
	loginFailedURL = "/?login=failed"
)

// GoogleLogin is what the routes need from the inbound adapter.
type GoogleLogin interface {
	Begin(ctx context.Context, state, nonce, verifier string) (string, error)
	Complete(ctx context.Context, code, nonce, verifier string) (google.Identity, error)
}

// IdentityStore is the durable binding between accounts and verified
// identities.
type IdentityStore interface {
	BindIdentity(ctx context.Context, accountID, issuer, subject string) error
	IdentityOf(ctx context.Context, accountID string) (issuer, subject string, bound bool, err error)
	AccountForIdentity(ctx context.Context, issuer, subject string) (string, bool, error)
	ResetIdentity(ctx context.Context, accountID string) error
}

// VerifierSealer seals the PKCE verifier for the minutes it sits in the
// database. It is the existing grant sealer; a second cipher would be a
// second thing to get wrong.
type VerifierSealer interface {
	Seal(record any, associatedData []byte) (json.RawMessage, error)
	Open(body json.RawMessage, associatedData []byte, record any) error
}

// handleGoogleStart mints the per-login secrets, records the transaction,
// binds it to this browser, and sends the browser to Google.
func handleGoogleStart(webConfig WebUIConfig, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, stateHash, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		nonce, _, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		verifier, _, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		browser, browserHash, err := session.NewToken()
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		sealed, err := webConfig.LoginSealer.Seal(verifier, []byte(stateHash))
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		expiresAt := now().Add(loginTTL)
		if err := webConfig.Sessions.CreateLoginTransaction(r.Context(), stateHash, browserHash, nonce, string(sealed), expiresAt); err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		target, err := webConfig.GoogleLogin.Begin(r.Context(), state, nonce, verifier)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, "could not start sign-in")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: loginCookie, Value: browser, Path: "/auth/google/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expiresAt,
		})
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// handleGoogleCallback consumes the transaction, verifies the identity, and
// either recognises a bound account or enrolls an allowlisted unbound one.
// Every failure lands on the same generic page: which check failed is
// logged, never shown, and no code or token is logged with it.
func handleGoogleCallback(webConfig WebUIConfig, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fail := func(reason string) {
			slog.Warn("google sign-in refused", "reason", reason)
			clearLoginCookie(w)
			http.Redirect(w, r, loginFailedURL, http.StatusSeeOther)
		}
		query := r.URL.Query()
		state := query.Get("state")
		cookie, err := r.Cookie(loginCookie)
		if err != nil || cookie.Value == "" || state == "" {
			fail("missing state or browser binding")
			return
		}
		// The transaction is consumed before anything else is checked, so a
		// denied or malformed callback still spends it: the state can never
		// be tried again with different parameters.
		nonce, sealed, err := webConfig.Sessions.ConsumeLoginTransaction(r.Context(), session.HashToken(state), session.HashToken(cookie.Value), now())
		if err != nil {
			fail("unknown, expired, replayed or foreign-browser transaction")
			return
		}
		if query.Get("error") != "" || query.Get("code") == "" {
			fail("provider returned no code")
			return
		}
		var verifier string
		if err := webConfig.LoginSealer.Open(json.RawMessage(sealed), []byte(session.HashToken(state)), &verifier); err != nil {
			fail("verifier unsealing failed")
			return
		}
		identity, err := webConfig.GoogleLogin.Complete(r.Context(), query.Get("code"), nonce, verifier)
		if err != nil {
			fail("identity verification failed")
			return
		}
		accountID, err := resolveIdentity(r.Context(), webConfig, identity)
		if err != nil {
			fail(err.Error())
			return
		}
		if err := issueAccountSession(webConfig, w, r, accountID, now()); err != nil {
			fail("could not create session")
			return
		}
		clearLoginCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// resolveIdentity decides which account a verified identity is. A bound
// identity is its account, provided that account is still configured. An
// unbound identity may enroll exactly the account whose configured address
// it presents, and only if that account has never enrolled -- a changed
// address on a bound account does not re-enroll anyone.
func resolveIdentity(ctx context.Context, webConfig WebUIConfig, identity google.Identity) (string, error) {
	if !identity.EmailVerified {
		return "", errors.New("email not verified by provider")
	}
	if bound, found, err := webConfig.Identities.AccountForIdentity(ctx, identity.Issuer, identity.Subject); err != nil {
		return "", errors.New("identity lookup failed")
	} else if found {
		if _, ok := webConfig.Accounts.Account(bound); !ok {
			return "", errors.New("bound account is no longer configured")
		}
		return bound, nil
	}
	account, ok := webConfig.Accounts.AccountForEmail(strings.ToLower(strings.TrimSpace(identity.Email)))
	if !ok {
		return "", errors.New("address is not on the allowlist")
	}
	if _, _, bound, err := webConfig.Identities.IdentityOf(ctx, account.ID); err != nil {
		return "", errors.New("identity lookup failed")
	} else if bound {
		return "", errors.New("account already enrolled with a different identity")
	}
	if err := webConfig.Identities.BindIdentity(ctx, account.ID, identity.Issuer, identity.Subject); err != nil {
		return "", errors.New("enrollment refused")
	}
	return account.ID, nil
}

func clearLoginCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: loginCookie, Value: "", Path: "/auth/google/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
