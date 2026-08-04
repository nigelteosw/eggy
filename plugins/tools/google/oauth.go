package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
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
)

// Auth holds the one grant every product call borrows from. Its mutex covers
// the read-modify-write of a login in progress; token refresh is serialized
// separately by the oauth2 token source it hands out.
type Auth struct {
	clientID     string
	clientSecret string
	scopes       []string
	store        *TokenStore
	client       *http.Client
	now          func() time.Time
	mu           sync.Mutex
}

func NewAuth(config Config, store *TokenStore, client *http.Client, now func() time.Time) *Auth {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	return &Auth{clientID: config.ClientID, clientSecret: config.ClientSecret, scopes: config.Scopes, store: store, client: client, now: now}
}

func (a *Auth) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID: a.clientID, ClientSecret: a.clientSecret, RedirectURL: LoopbackRedirect, Scopes: a.scopes,
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
	return a.store.Update(func(stored *TokenRecord) error {
		applyToken(stored, token)
		stored.State, stored.CodeVerifier, stored.StateExpires = "", "", time.Time{}
		return nil
	})
}

func (a *Auth) Logout() error { return a.store.Delete() }

// Status reports what the owner needs to decide whether to re-authorize, and
// deliberately returns no token material.
func (a *Auth) Status() (authorized bool, scopes []string, expiry time.Time, err error) {
	record, err := a.store.Load()
	if err != nil {
		return false, nil, time.Time{}, err
	}
	return record.Authorized(), record.Scopes, record.Expiry, nil
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
	return oauth2.NewClient(ctx, &persistingSource{source: source, store: a.store}), nil
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
