package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var ErrLoginRequired = errors.New("MCP login required")

type oauthProvider struct {
	config ServerConfig
	store  *OAuthStore
	client *http.Client
	mu     sync.Mutex
}

var _ auth.OAuthHandler = (*oauthProvider)(nil)

func newOAuthProvider(config ServerConfig, store *OAuthStore, client *http.Client) *oauthProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &oauthProvider{config: config, store: store, client: client}
}

func (p *oauthProvider) BeginLogin(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return "", errors.New("MCP OAuth storage is unavailable")
	}
	// The redirect is built from server.public_base_url, so an unset base leaves
	// a path with no scheme or host. The authorization server rejects that with
	// its own generic complaint about the client, which points nowhere near the
	// setting that is actually missing.
	if redirect, err := url.Parse(p.config.RedirectURL); err != nil || !redirect.IsAbs() || redirect.Host == "" {
		return "", errors.New("MCP OAuth redirect URL is not absolute; set server.public_base_url to the address the browser can reach")
	}
	record, err := p.store.Load(p.config.Name, p.config.URL)
	if err != nil && !errors.Is(err, ErrOAuthRecordNotFound) {
		return "", err
	}
	if errors.Is(err, ErrOAuthRecordNotFound) {
		record = OAuthRecord{Version: 1, ServerURL: p.config.URL}
	}
	if record.AuthorizationEndpoint == "" || record.TokenEndpoint == "" {
		if err := p.discover(ctx, &record); err != nil {
			return "", err
		}
	}
	// A configured client wins over anything stored: it is how the owner
	// corrects a client registered against the wrong project, and it is the
	// only path that works at all against an authorization server -- Google's
	// among them -- that does not implement RFC 7591 registration.
	if p.config.OAuthClientID != "" {
		record.ClientID = p.config.OAuthClientID
		record.ClientSecret = p.config.OAuthClientSecret
		record.TokenEndpointAuthMethod = ""
		if p.config.OAuthClientSecret != "" {
			record.TokenEndpointAuthMethod = "client_secret_post"
		}
	}
	// Configured scopes win at every login, not only at discovery. A completed
	// exchange overwrites the record's scopes with the ones actually granted,
	// so without this a scope added to config.yaml after the first login would
	// never be asked for again -- the flow would keep replaying the old grant
	// and the new capability would fail at call time with a 403.
	if len(p.config.OAuthScopes) > 0 {
		record.Scopes = append([]string(nil), p.config.OAuthScopes...)
	}
	if record.ClientID == "" {
		if record.RegistrationEndpoint == "" {
			return "", errors.New("MCP authorization server does not support dynamic client registration; set oauth_client_id and oauth_client_secret_env for a client you registered by hand")
		}
		registration, err := oauthex.RegisterClient(ctx, record.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
			RedirectURIs: []string{p.config.RedirectURL}, TokenEndpointAuthMethod: "client_secret_post",
			GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
			ClientName: "Eggy", Scope: strings.Join(record.Scopes, " "),
		}, p.client)
		if err != nil {
			return "", fmt.Errorf("register MCP OAuth client: %w", err)
		}
		record.ClientID = registration.ClientID
		record.ClientSecret = registration.ClientSecret
		record.TokenEndpointAuthMethod = registration.TokenEndpointAuthMethod
	}
	state, err := randomOAuthValue(32)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	record.State = state
	record.StateExpires = time.Now().Add(10 * time.Minute)
	record.CodeVerifier = verifier
	config := oauthConfig(record, p.config.RedirectURL)
	// prompt=consent alongside access_type=offline is what actually guarantees
	// a refresh token from Google: offline alone returns one only on the
	// account's first consent to this client, so a re-authorization after any
	// earlier grant would hand back an access token that cannot be renewed.
	// The cost is seeing the consent screen on every login, which is rare.
	authorizationURL := config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("resource", record.Resource))
	record.LastAuthorizationURL = authorizationURL
	if err := p.store.Save(p.config.Name, p.config.URL, record); err != nil {
		return "", err
	}
	return authorizationURL, nil
}

func (p *oauthProvider) CompleteLogin(ctx context.Context, code, state string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, err := p.store.Load(p.config.Name, p.config.URL)
	if err != nil {
		return ErrLoginRequired
	}
	if record.State == "" || record.StateExpires.IsZero() || time.Now().After(record.StateExpires) {
		return errors.New("no pending MCP login, or it expired; start the login again")
	}
	// An empty state is the owner pasting the redirect back by hand, which is
	// the only path that works when the browser cannot reach Eggy's callback.
	// It is not a weakening of the CSRF check the callback route relies on:
	// that route rejects an empty state before it ever gets here, and a pasted
	// value still has to land inside the ten-minute pending window the owner
	// themselves opened.
	if state != "" && state != record.State {
		return errors.New("MCP OAuth state does not match the pending login; start the login again")
	}
	if strings.TrimSpace(code) == "" {
		return errors.New("MCP OAuth code is required")
	}
	config := oauthConfig(record, p.config.RedirectURL)
	token, err := config.Exchange(oauthHTTPContext(ctx, p.client), code, oauth2.VerifierOption(record.CodeVerifier), oauth2.SetAuthURLParam("resource", record.Resource))
	if err != nil {
		// The authorization server's own words. redirect_uri_mismatch,
		// invalid_client and an expired code are four different repairs, and a
		// flat "exchange failed" sent the owner looking at all of them.
		return fmt.Errorf("MCP OAuth code exchange failed: %w", err)
	}
	copyTokenToRecord(&record, token)
	record.State = ""
	record.StateExpires = time.Time{}
	record.CodeVerifier = ""
	record.LastAuthorizationURL = ""
	return p.store.Save(p.config.Name, p.config.URL, record)
}

func (p *oauthProvider) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	record, err := p.store.Load(p.config.Name, p.config.URL)
	if errors.Is(err, ErrOAuthRecordNotFound) || record.AccessToken == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	token := &oauth2.Token{AccessToken: record.AccessToken, RefreshToken: record.RefreshToken, TokenType: record.TokenType, Expiry: record.Expiry}
	source := oauthConfig(record, p.config.RedirectURL).TokenSource(oauthHTTPContext(ctx, p.client), token)
	return &persistingTokenSource{source: source, provider: p}, nil
}

func (p *oauthProvider) Authorize(_ context.Context, _ *http.Request, response *http.Response) error {
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	return ErrLoginRequired
}

func (p *oauthProvider) Logout() error {
	return p.store.Delete(p.config.Name, p.config.URL)
}

func (p *oauthProvider) discover(ctx context.Context, record *OAuthRecord) error {
	metadata, err := p.protectedResourceMetadata(ctx)
	if err != nil {
		return fmt.Errorf("discover MCP protected resource: %w", err)
	}
	if len(metadata.AuthorizationServers) == 0 {
		return errors.New("MCP protected resource has no authorization server")
	}
	server, err := p.authorizationServerMetadata(ctx, metadata.AuthorizationServers[0])
	if err != nil {
		return fmt.Errorf("discover MCP authorization server: %w", err)
	}
	if server == nil {
		issuer := strings.TrimRight(metadata.AuthorizationServers[0], "/")
		server = &oauthex.AuthServerMeta{Issuer: issuer, AuthorizationEndpoint: issuer + "/authorize", TokenEndpoint: issuer + "/token", RegistrationEndpoint: issuer + "/register"}
	}
	record.Resource = metadata.Resource
	record.AuthorizationEndpoint = server.AuthorizationEndpoint
	record.TokenEndpoint = server.TokenEndpoint
	record.RegistrationEndpoint = server.RegistrationEndpoint
	if len(p.config.OAuthScopes) > 0 {
		record.Scopes = append([]string(nil), p.config.OAuthScopes...)
	} else if len(metadata.ScopesSupported) > 0 {
		record.Scopes = append([]string(nil), metadata.ScopesSupported...)
	} else {
		record.Scopes = append([]string(nil), server.ScopesSupported...)
	}
	return nil
}

// authorizationServerMetadata fetches the authorization server's metadata,
// tolerating a trailing slash the protected resource added to the issuer.
//
// RFC 8414 section 3.3 requires the issuer inside the metadata document to
// equal the issuer identifier used to build its URL, byte for byte, and the
// SDK enforces that. Google's remote MCP servers advertise
// "https://accounts.google.com/" in authorization_servers while the document
// at that address declares "https://accounts.google.com", so handing the
// advertised string straight to discovery fails on the slash alone and no
// Google MCP server can ever be authorized. Both spellings address the same
// metadata document, so trying the trimmed form second costs one request in
// the failure case and nothing in the common one -- and a server that really
// does publish a trailing-slash issuer still matches on the first attempt.
func (p *oauthProvider) authorizationServerMetadata(ctx context.Context, advertised string) (*oauthex.AuthServerMeta, error) {
	candidates := []string{advertised}
	if trimmed := strings.TrimRight(advertised, "/"); trimmed != "" && trimmed != advertised {
		candidates = append(candidates, trimmed)
	}
	var last error
	for _, issuer := range candidates {
		server, err := auth.GetAuthServerMetadata(ctx, issuer, p.client)
		if err != nil {
			last = err
			continue
		}
		if server != nil {
			return server, nil
		}
	}
	// A nil server with no error means every candidate answered 4xx, which the
	// caller handles by synthesizing conventional endpoints. Only a real
	// failure is worth reporting.
	return nil, last
}

func (p *oauthProvider) protectedResourceMetadata(ctx context.Context) (*oauthex.ProtectedResourceMetadata, error) {
	resource, err := url.Parse(p.config.URL)
	if err != nil {
		return nil, err
	}
	endpoint := *resource
	endpoint.Path = "/.well-known/oauth-protected-resource/" + strings.TrimLeft(resource.Path, "/")
	candidates := []struct{ endpoint, resource string }{{endpoint.String(), p.config.URL}}
	endpoint.Path = "/.well-known/oauth-protected-resource"
	root := *resource
	root.Path, root.RawPath, root.RawQuery, root.Fragment = "", "", "", ""
	candidates = append(candidates, struct{ endpoint, resource string }{endpoint.String(), root.String()})
	var last error
	for _, candidate := range candidates {
		metadata, err := oauthex.GetProtectedResourceMetadata(ctx, candidate.endpoint, candidate.resource, p.client)
		if err == nil {
			return metadata, nil
		}
		last = err
	}
	return nil, last
}

func oauthConfig(record OAuthRecord, redirectURL string) *oauth2.Config {
	style := oauth2.AuthStyleAutoDetect
	if record.TokenEndpointAuthMethod == "client_secret_post" {
		style = oauth2.AuthStyleInParams
	} else if record.TokenEndpointAuthMethod == "client_secret_basic" {
		style = oauth2.AuthStyleInHeader
	}
	return &oauth2.Config{ClientID: record.ClientID, ClientSecret: record.ClientSecret, RedirectURL: redirectURL, Scopes: record.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: record.AuthorizationEndpoint, TokenURL: record.TokenEndpoint, AuthStyle: style}}
}

func oauthHTTPContext(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

func randomOAuthValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// copyTokenToRecord stores a freshly issued token, keeping the refresh token
// already on record when the response carries none.
//
// An authorization server is not required to return a refresh token every
// time. Google omits it when the account has already consented to this client,
// and returns one only on first consent or when consent is forced. Copying the
// empty value over a good one turns a working connection into one that dies
// silently at the first access-token expiry, roughly an hour later, with
// "login required" and no indication that anything was discarded.
//
// The scopes stored are the ones the response reports as granted, not the ones
// asked for. A consent screen lets the account untick individual permissions,
// and a record claiming a scope the grant does not carry produces a 403 at
// tool-call time that reads like a broken server rather than a narrow consent.
func copyTokenToRecord(record *OAuthRecord, token *oauth2.Token) {
	if granted, ok := token.Extra("scope").(string); ok && strings.TrimSpace(granted) != "" {
		record.Scopes = strings.Fields(granted)
	}
	record.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		record.RefreshToken = token.RefreshToken
	}
	record.TokenType = token.TokenType
	record.Expiry = token.Expiry
}

type persistingTokenSource struct {
	source   oauth2.TokenSource
	provider *oauthProvider
}

type bearerHandler struct {
	tokenSource oauth2.TokenSource
}

func newBearerHandler(token string) *bearerHandler {
	return &bearerHandler{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})}
}

func (h *bearerHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return h.tokenSource, nil
}

func (h *bearerHandler) Authorize(_ context.Context, _ *http.Request, response *http.Response) error {
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	return errors.New("MCP bearer token was rejected")
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	err = s.provider.store.Update(s.provider.config.Name, s.provider.config.URL, func(record *OAuthRecord) error {
		copyTokenToRecord(record, token)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return token, nil
}
