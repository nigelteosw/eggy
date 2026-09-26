package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// LoopbackRedirect is why this path works where the MCP one did not.
//
// Google grants desktop (installed-app) clients an implicit loopback redirect
// on any port, so nothing has to be registered in the console, nothing has to
// be publicly reachable, and server.public_base_url is irrelevant to
// authorization. Port 1 is deliberately dead: no listener is ever started, the
// browser fails to connect, and the address bar still holds the code. That is
// exactly what Hermes' setup.py does, and the pasted-redirect completion below
// is what makes a dead port sufficient.
//
// It must match byte for byte between the authorization request and the token
// exchange, so it is a constant rather than anything derived from config.
const LoopbackRedirect = "http://localhost:1"

// pendingWindow bounds a login in progress. Ten minutes is the same window the
// MCP adapter uses, and comfortably longer than an owner needs to approve in a
// browser and paste back.
const pendingWindow = 10 * time.Minute

// Endpoints are variables, not constants, so tests can point the flow at a
// local server. They stay unexported and package-scoped deliberately, which is
// what makes "nothing in config may set them" the compiler's job rather than a
// review convention: a settable token host redirects the client secret and
// every authorization code to whatever address the config names. Google's
// endpoints are the same for every owner, so there is no caller to serve by
// exporting them. Decided in AGENTS.md; do not promote these to Config.
var (
	authorizationEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint         = "https://oauth2.googleapis.com/token"
	// identityEndpoint is Google's OpenID Connect userinfo endpoint: the one
	// place the grant's own identity is read from, so a token issued to a
	// person's account is recognised before it can be stored as Eggy's.
	identityEndpoint = "https://openidconnect.googleapis.com/v1/userinfo"
)

// identityScopes are requested alongside the product scopes so the identity
// endpoint answers. They grant nothing beyond reading who the grant is for.
var identityScopes = []string{"openid", "https://www.googleapis.com/auth/userinfo.email"}

// ErrIdentityMismatch reports a grant that belongs to someone other than the
// configured Eggy identity. Tools refuse to run on it and nothing is deleted:
// the repair is a reconnect as the right account, not a loss of the grant.
var ErrIdentityMismatch = errors.New("the Google connection is not Eggy's own account")

// Auth holds the one grant every product call borrows from. Its mutex covers
// the read-modify-write of a login in progress; token refresh is serialized
// separately by the oauth2 token source it hands out.
type Auth struct {
	clientID      string
	clientSecret  string
	expectedEmail string
	scopes        []string
	store         *TokenStore
	client        *http.Client
	now           func() time.Time
	mu            sync.Mutex
}

func NewAuth(config Config, store *TokenStore, client *http.Client, now func() time.Time) *Auth {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	return &Auth{clientID: config.ClientID, clientSecret: config.ClientSecret, expectedEmail: strings.ToLower(strings.TrimSpace(config.ExpectedEmail)),
		scopes: config.Scopes, store: store, client: client, now: now}
}

func (a *Auth) config() *oauth2.Config {
	scopes := append(append([]string(nil), a.scopes...), identityScopes...)
	return &oauth2.Config{
		ClientID: a.clientID, ClientSecret: a.clientSecret, RedirectURL: LoopbackRedirect, Scopes: scopes,
		Endpoint: oauth2.Endpoint{AuthURL: authorizationEndpoint, TokenURL: tokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
	}
}

// BeginLogin returns the URL the owner approves in a browser.
//
// access_type=offline with prompt=consent is what actually guarantees a
// refresh token: offline alone returns one only on the account's first consent
// to this client, so a re-authorization after any earlier grant would hand
// back an access token that cannot be renewed. The same lesson the MCP adapter
// records, and Hermes forces consent for the same reason.
func (a *Auth) BeginLogin(context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clientID == "" {
		return "", errors.New("google.client_id is not configured")
	}
	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return "", err
	}
	if err := a.store.Update(func(record *TokenRecord) error {
		record.State, record.CodeVerifier, record.StateExpires = state, verifier, a.now().Add(pendingWindow)
		return nil
	}); err != nil {
		return "", err
	}
	return a.config().AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// CompleteLogin exchanges a code the owner carried back by hand.
//
// State is checked only when the paste carried one, because a bare code has
// none to carry. What bounds the exchange is the pending window the owner
// themselves opened minutes earlier, not an echoed parameter -- and unlike the
// MCP callback route, nothing unauthenticated can reach this.
func (a *Auth) CompleteLogin(ctx context.Context, code, state string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.store.Load()
	if err != nil {
		return err
	}
	if record.State == "" || record.StateExpires.IsZero() || a.now().After(record.StateExpires) {
		return errors.New("no pending Google login, or it expired; run the login again")
	}
	if state != "" && state != record.State {
		return errors.New("that redirect belongs to a different login; run the login again")
	}
	if strings.TrimSpace(code) == "" {
		return errors.New("an authorization code is required")
	}
	token, err := a.config().Exchange(oauthContext(ctx, a.client), code, oauth2.VerifierOption(record.CodeVerifier))
	if err != nil {
		// Google's own words. An expired code, a client secret that does not
		// match the client id, and a consent screen the account is not a test
		// user on are three different repairs.
		return fmt.Errorf("Google token exchange failed: %w", err)
	}
	if token.RefreshToken == "" && record.RefreshToken == "" {
		return errors.New("Google returned no refresh token; revoke Eggy's access at myaccount.google.com/permissions and authorize again")
	}
	// Whose grant is this? Asked of Google with the new token before anything
	// is written, so a user who tapped through the consent screen as
	// themselves is told so and the existing connection -- Eggy's -- is kept
	// exactly as it was, pending login cleared.
	identity, err := a.identity(ctx, token.AccessToken)
	if err != nil {
		_ = a.clearPending()
		return err
	}
	if err := a.checkExpected(identity); err != nil {
		_ = a.clearPending()
		return err
	}
	return a.store.Update(func(stored *TokenRecord) error {
		applyToken(stored, token)
		stored.State, stored.CodeVerifier, stored.StateExpires = "", "", time.Time{}
		stored.Email, stored.Subject = identity.email, identity.subject
		stored.Generation++
		return nil
	})
}

func (a *Auth) clearPending() error {
	return a.store.Update(func(stored *TokenRecord) error {
		stored.State, stored.CodeVerifier, stored.StateExpires = "", "", time.Time{}
		return nil
	})
}

// Logout disconnects: tokens and identity go, the generation advances and
// stays, so an approval granted against the old connection cannot be
// consumed by the next one.
func (a *Auth) Logout() error {
	return a.store.Update(func(stored *TokenRecord) error {
		generation := stored.Generation + 1
		*stored = TokenRecord{Version: 1, Generation: generation}
		return nil
	})
}

// Generation is the current connection number, for approvals to bind to.
// An unreadable record reports 0, which no approval granted after a
// connection carries.
func (a *Auth) Generation() uint64 {
	record, err := a.store.Load()
	if err != nil {
		return 0
	}
	return record.Generation
}

// Status is what a surface shows about the connection. It deliberately
// carries no token material.
type Status struct {
	Authorized bool      `json:"authorized"`
	Scopes     []string  `json:"scopes,omitempty"`
	Expiry     time.Time `json:"expiry,omitzero"`
	// Email is the verified address the grant belongs to; empty for a grant
	// written before verification existed and not yet verified.
	Email string `json:"email,omitempty"`
	// ExpectedEmail is the configured Eggy identity, so a surface can show
	// a mismatch beside the connection instead of a bare "authorized".
	ExpectedEmail string `json:"expected_email,omitempty"`
	Generation    uint64 `json:"generation"`
}

// Status reports what the owner needs to decide whether to re-authorize.
func (a *Auth) Status() (Status, error) {
	record, err := a.store.Load()
	if err != nil {
		return Status{}, err
	}
	return Status{Authorized: record.Authorized(), Scopes: record.Scopes, Expiry: record.Expiry,
		Email: record.Email, ExpectedEmail: a.expectedEmail, Generation: record.Generation}, nil
}

type grantIdentity struct {
	email, subject string
}

// identity asks Google's identity endpoint who accessToken belongs to.
func (a *Auth) identity(ctx context.Context, accessToken string) (grantIdentity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, identityEndpoint, nil)
	if err != nil {
		return grantIdentity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := a.client.Do(request)
	if err != nil {
		return grantIdentity{}, fmt.Errorf("verify Google identity: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return grantIdentity{}, fmt.Errorf("verify Google identity: Google answered %s", response.Status)
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&claims); err != nil {
		return grantIdentity{}, fmt.Errorf("verify Google identity: %w", err)
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if claims.Subject == "" || email == "" || !claims.EmailVerified {
		return grantIdentity{}, errors.New("verify Google identity: Google did not report a verified address for this grant")
	}
	return grantIdentity{email: email, subject: claims.Subject}, nil
}

// checkExpected is the guard against a personal account becoming Eggy's.
func (a *Auth) checkExpected(identity grantIdentity) error {
	if a.expectedEmail == "" || identity.email == a.expectedEmail {
		return nil
	}
	return fmt.Errorf("%w: that consent screen was signed in as %s, but Eggy's account is %s; sign in as Eggy and try again (the existing connection was kept)", ErrIdentityMismatch, identity.email, a.expectedEmail)
}

// verified makes sure a grant written before identities were recorded
// belongs to the expected account before it serves a tool. Verified once
// and written back; a mismatch is refused every time and deletes nothing.
func (a *Auth) verified(ctx context.Context, record TokenRecord, source oauth2.TokenSource) error {
	if record.Email != "" || a.expectedEmail == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	token, err := source.Token()
	if err != nil {
		return err
	}
	identity, err := a.identity(ctx, token.AccessToken)
	if err != nil {
		return err
	}
	if err := a.checkExpected(identity); err != nil {
		return err
	}
	return a.store.Update(func(stored *TokenRecord) error {
		stored.Email, stored.Subject = identity.email, identity.subject
		return nil
	})
}

// Client returns an HTTP client that renews and re-persists the token as
// needed. Every product call in this package goes through it, which is the
// whole point of one grant: adding a product adds requests, not a second
// authorization.
func (a *Auth) Client(ctx context.Context) (*http.Client, error) {
	record, err := a.store.Load()
	if err != nil {
		return nil, err
	}
	if !record.Authorized() {
		return nil, ErrNotAuthorized
	}
	token := &oauth2.Token{AccessToken: record.AccessToken, RefreshToken: record.RefreshToken, TokenType: record.TokenType, Expiry: record.Expiry}
	source := a.config().TokenSource(oauthContext(ctx, a.client), token)
	persisting := &persistingSource{source: source, store: a.store}
	if err := a.verified(ctx, record, persisting); err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, persisting), nil
}

// persistingSource writes a renewed token back before it is used. Without
// this, every restart falls back to the last token written at login and burns
// a refresh on the first call.
type persistingSource struct {
	source oauth2.TokenSource
	store  *TokenStore
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if err != nil {
		// A revoked grant is not a transient error, and it is the one failure
		// the owner has to act on rather than retry.
		return nil, fmt.Errorf("%w: %v", ErrNotAuthorized, err)
	}
	if err := s.store.Update(func(record *TokenRecord) error {
		applyToken(record, token)
		return nil
	}); err != nil {
		return nil, err
	}
	return token, nil
}

// applyToken keeps a refresh token the response omitted, and records the
// scopes actually granted rather than the ones requested.
//
// Google omits the refresh token whenever the account has already consented,
// so copying an empty value over a good one turns a working connection into
// one that dies at the next expiry. It reports "scope" on the grant, and a
// record claiming a permission the consent screen dropped produces a 403 at
// call time that reads like a broken API.
func applyToken(record *TokenRecord, token *oauth2.Token) {
	record.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		record.RefreshToken = token.RefreshToken
	}
	record.TokenType = token.TokenType
	record.Expiry = token.Expiry
	if granted, ok := token.Extra("scope").(string); ok && strings.TrimSpace(granted) != "" {
		record.Scopes = strings.Fields(granted)
	}
}

func randomState() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func oauthContext(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}
