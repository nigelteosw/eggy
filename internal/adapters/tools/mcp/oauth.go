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
	if record.ClientID == "" {
		if record.RegistrationEndpoint == "" {
			return "", errors.New("MCP authorization server does not support dynamic client registration")
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
	authorizationURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", record.Resource))
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
	if state == "" || state != record.State || record.StateExpires.IsZero() || time.Now().After(record.StateExpires) {
		return errors.New("invalid or expired MCP OAuth state")
	}
	if strings.TrimSpace(code) == "" {
		return errors.New("MCP OAuth code is required")
	}
	config := oauthConfig(record, p.config.RedirectURL)
	token, err := config.Exchange(oauthHTTPContext(ctx, p.client), code, oauth2.VerifierOption(record.CodeVerifier), oauth2.SetAuthURLParam("resource", record.Resource))
	if err != nil {
		return errors.New("MCP OAuth code exchange failed")
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
	server, err := auth.GetAuthServerMetadata(ctx, metadata.AuthorizationServers[0], p.client)
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

func copyTokenToRecord(record *OAuthRecord, token *oauth2.Token) {
	record.AccessToken = token.AccessToken
	record.RefreshToken = token.RefreshToken
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
