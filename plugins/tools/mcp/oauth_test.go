package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestOAuthProviderDiscoversRegistersExchangesAndRestores(t *testing.T) {
	store, err := OpenOAuthStore(authPath(t), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := &oauthRoundTripper{}
	client := &http.Client{Transport: roundTrip}
	cfg := ServerConfig{Name: "railway", URL: "https://resource.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback", OAuthScopes: []string{"project:read"}}
	provider := newOAuthProvider(cfg, store, client)
	authorizationURL, err := provider.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.String() == "" || query.Get("client_id") != "dynamic-client" || query.Get("state") == "" || query.Get("code_challenge_method") != "S256" || query.Get("resource") != cfg.URL {
		t.Fatalf("authorization URL=%s", authorizationURL)
	}
	if err := provider.CompleteLogin(context.Background(), "authorization-code", query.Get("state")); err != nil {
		t.Fatal(err)
	}
	if roundTrip.exchangeVerifier == "" || roundTrip.exchangeCode != "authorization-code" {
		t.Fatalf("exchange code=%q verifier=%q", roundTrip.exchangeCode, roundTrip.exchangeVerifier)
	}
	restored := newOAuthProvider(cfg, store, client)
	tokenSource, err := restored.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokenSource.Token()
	if err != nil || token.AccessToken != "access-token" || token.RefreshToken != "refresh-token" {
		t.Fatalf("token=%#v err=%v", token, err)
	}
}

type oauthRoundTripper struct {
	exchangeCode     string
	exchangeVerifier string
}

func (r *oauthRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response := func(status int, body string) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	switch request.URL.String() {
	case "https://resource.example/.well-known/oauth-protected-resource":
		return response(http.StatusOK, `{"resource":"https://resource.example","authorization_servers":["https://auth.example"]}`)
	case "https://auth.example/.well-known/oauth-authorization-server":
		return response(http.StatusOK, `{"issuer":"https://auth.example","authorization_endpoint":"https://auth.example/authorize","token_endpoint":"https://auth.example/token","registration_endpoint":"https://auth.example/register","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`)
	case "https://auth.example/register":
		return response(http.StatusCreated, `{"client_id":"dynamic-client","client_secret":"dynamic-secret","redirect_uris":["https://eggy.example/auth/mcp/railway/callback"],"token_endpoint_auth_method":"client_secret_post"}`)
	case "https://auth.example/token":
		body, _ := io.ReadAll(request.Body)
		values, _ := url.ParseQuery(string(body))
		if values.Get("grant_type") == "refresh_token" {
			return response(http.StatusOK, `{"access_token":"refreshed-access","refresh_token":"rotated-refresh","token_type":"Bearer","expires_in":3600}`)
		}
		r.exchangeCode = values.Get("code")
		r.exchangeVerifier = values.Get("code_verifier")
		return response(http.StatusOK, `{"access_token":"access-token","refresh_token":"refresh-token","token_type":"Bearer","expires_in":3600}`)
	default:
		encoded, _ := json.Marshal(request.URL.String())
		return response(http.StatusNotFound, string(encoded))
	}
}

func TestOAuthHandlerAuthorizeReturnsLoginRequired(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	provider := newOAuthProvider(ServerConfig{Name: "railway", URL: "https://resource.example"}, store, http.DefaultClient)
	response := &http.Response{Body: io.NopCloser(strings.NewReader("unauthorized"))}
	if err := provider.Authorize(context.Background(), nil, response); err != ErrLoginRequired {
		t.Fatalf("error=%v", err)
	}
}

func TestOAuthProviderRejectsMismatchedState(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	provider := newOAuthProvider(ServerConfig{Name: "railway", URL: "https://resource.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback"}, store, &http.Client{Transport: &oauthRoundTripper{}})
	if _, err := provider.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.CompleteLogin(context.Background(), "code", "wrong-state"); err == nil {
		t.Fatal("mismatched OAuth state accepted")
	}
}

func TestOAuthProviderPersistsRotatedRefreshToken(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	client := &http.Client{Transport: &oauthRoundTripper{}}
	cfg := ServerConfig{Name: "railway", URL: "https://resource.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback"}
	record := OAuthRecord{
		Version: 1, ServerURL: cfg.URL, ClientID: "dynamic-client", ClientSecret: "dynamic-secret",
		TokenEndpoint: "https://auth.example/token", TokenEndpointAuthMethod: "client_secret_post",
		AccessToken: "expired", RefreshToken: "old-refresh", TokenType: "Bearer", Expiry: time.Now().Add(-time.Hour),
	}
	if err := store.Save(cfg.Name, cfg.URL, record); err != nil {
		t.Fatal(err)
	}
	provider := newOAuthProvider(cfg, store, client)
	source, err := provider.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token, err := source.Token()
	if err != nil || token.RefreshToken != "rotated-refresh" {
		t.Fatalf("token=%#v err=%v", token, err)
	}
	stored, err := store.Load(cfg.Name, cfg.URL)
	if err != nil || stored.AccessToken != "refreshed-access" || stored.RefreshToken != "rotated-refresh" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

// googleIssuerRoundTripper reproduces what Google's remote MCP servers
// actually serve: the protected resource advertises its authorization server
// with a trailing slash, while the metadata document at that address declares
// the issuer without one. RFC 8414 requires the two to match exactly, so the
// advertised string cannot be handed to discovery unchanged.
type googleIssuerRoundTripper struct{ metadataURLs []string }

func (r *googleIssuerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response := func(status int, body string) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	url := request.URL.String()
	switch url {
	case "https://calendarmcp.googleapis.com/.well-known/oauth-protected-resource/mcp/v1",
		"https://calendarmcp.googleapis.com/.well-known/oauth-protected-resource":
		return response(http.StatusOK, `{"resource":"https://calendarmcp.googleapis.com/mcp/v1","authorization_servers":["https://accounts.google.com/"]}`)
	}
	if strings.Contains(url, "/.well-known/") {
		r.metadataURLs = append(r.metadataURLs, url)
		return response(http.StatusOK, `{"issuer":"https://accounts.google.com","authorization_endpoint":"https://accounts.google.com/o/oauth2/v2/auth","token_endpoint":"https://oauth2.googleapis.com/token","registration_endpoint":"https://accounts.google.com/register","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`)
	}
	return response(http.StatusNotFound, `{}`)
}

func TestOAuthDiscoveryToleratesTrailingSlashIssuer(t *testing.T) {
	store, err := OpenOAuthStore(authPath(t), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := &googleIssuerRoundTripper{}
	provider := newOAuthProvider(ServerConfig{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1",
		RedirectURL: "https://eggy.example/auth/mcp/calendar/callback",
	}, store, &http.Client{Transport: roundTrip})

	record := OAuthRecord{}
	if err := provider.discover(context.Background(), &record); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if record.AuthorizationEndpoint != "https://accounts.google.com/o/oauth2/v2/auth" || record.TokenEndpoint != "https://oauth2.googleapis.com/token" {
		t.Fatalf("record=%#v", record)
	}
	if record.Resource != "https://calendarmcp.googleapis.com/mcp/v1" {
		t.Fatalf("resource=%q", record.Resource)
	}
}

// TestOAuthUsesPreRegisteredClientWhenRegistrationIsUnsupported covers the
// authorization servers that do not implement RFC 7591 -- Google's among them.
// Discovery returns no registration endpoint, so without a configured client
// there is nothing to authorize with and BeginLogin fails outright.
func TestOAuthUsesPreRegisteredClientWhenRegistrationIsUnsupported(t *testing.T) {
	store, err := OpenOAuthStore(authPath(t), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &noRegistrationRoundTripper{}}
	cfg := ServerConfig{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1",
		RedirectURL: "https://eggy.example/auth/mcp/calendar/callback",
	}

	unregistered := newOAuthProvider(cfg, store, client)
	_, err = unregistered.BeginLogin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "oauth_client_id") {
		t.Fatalf("error=%v, want one naming the fix", err)
	}

	cfg.OAuthClientID = "eggy.apps.googleusercontent.com"
	cfg.OAuthClientSecret = "configured-secret"
	provider := newOAuthProvider(cfg, store, client)
	authorizationURL, err := provider.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("client_id") != cfg.OAuthClientID {
		t.Fatalf("authorization URL=%s", authorizationURL)
	}
	record, err := store.Load(cfg.Name, cfg.URL)
	if err != nil {
		t.Fatal(err)
	}
	if record.ClientSecret != "configured-secret" || record.TokenEndpointAuthMethod != "client_secret_post" {
		t.Fatalf("record=%#v", record)
	}
}

// A client ID with no secret is a public client: PKCE alone authorizes it, and
// no auth method may be asserted at the token endpoint.
func TestOAuthPreRegisteredPublicClientHasNoSecret(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	cfg := ServerConfig{
		Name: "calendar", URL: "https://calendarmcp.googleapis.com/mcp/v1",
		RedirectURL:   "https://eggy.example/auth/mcp/calendar/callback",
		OAuthClientID: "public-client",
	}
	provider := newOAuthProvider(cfg, store, &http.Client{Transport: &noRegistrationRoundTripper{}})
	if _, err := provider.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, _ := store.Load(cfg.Name, cfg.URL)
	if record.ClientSecret != "" || record.TokenEndpointAuthMethod != "" {
		t.Fatalf("record=%#v", record)
	}
}

// noRegistrationRoundTripper serves metadata with no registration_endpoint,
// which is what an authorization server without dynamic client registration
// publishes.
type noRegistrationRoundTripper struct{}

func (noRegistrationRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	body := `{}`
	switch {
	case strings.Contains(request.URL.Path, "/.well-known/oauth-protected-resource"):
		body = `{"resource":"https://calendarmcp.googleapis.com/mcp/v1","authorization_servers":["https://accounts.google.com"]}`
	case strings.Contains(request.URL.Path, "/.well-known/"):
		body = `{"issuer":"https://accounts.google.com","authorization_endpoint":"https://accounts.google.com/o/oauth2/v2/auth","token_endpoint":"https://oauth2.googleapis.com/token","response_types_supported":["code"],"code_challenge_methods_supported":["S256"]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

// TestCompleteLoginKeepsAnExistingRefreshTokenWhenNoneIsReturned covers the
// failure that looks like a working connection: the server issues an access
// token with no refresh token (Google does this whenever the account has
// already consented), and discarding the stored one leaves a session that dies
// at the first expiry with "login required".
func TestCompleteLoginKeepsAnExistingRefreshTokenWhenNoneIsReturned(t *testing.T) {
	record := OAuthRecord{RefreshToken: "long-lived-refresh", AccessToken: "old-access"}
	copyTokenToRecord(&record, &oauth2.Token{AccessToken: "new-access", TokenType: "Bearer"})
	if record.RefreshToken != "long-lived-refresh" {
		t.Fatalf("refresh token discarded: %#v", record)
	}
	if record.AccessToken != "new-access" {
		t.Fatalf("record=%#v", record)
	}

	copyTokenToRecord(&record, &oauth2.Token{AccessToken: "newer", RefreshToken: "rotated", TokenType: "Bearer"})
	if record.RefreshToken != "rotated" {
		t.Fatalf("a rotated refresh token must replace the old one: %#v", record)
	}
}

// Offline access alone is not enough to be handed a refresh token; consent has
// to be forced, or a second authorization returns an access token that cannot
// be renewed.
func TestBeginLoginForcesConsentSoARefreshTokenIsIssued(t *testing.T) {
	store, _ := OpenOAuthStore(authPath(t), testEncryptionKey())
	provider := newOAuthProvider(ServerConfig{
		Name: "railway", URL: "https://resource.example",
		RedirectURL: "https://eggy.example/auth/mcp/railway/callback",
	}, store, &http.Client{Transport: &oauthRoundTripper{}})

	authorizationURL, err := provider.BeginLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if query.Query().Get("access_type") != "offline" || query.Query().Get("prompt") != "consent" {
		t.Fatalf("authorization URL=%s", authorizationURL)
	}
}
