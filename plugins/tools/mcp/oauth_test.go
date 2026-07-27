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
)

func TestOAuthProviderDiscoversRegistersExchangesAndRestores(t *testing.T) {
	store, err := OpenOAuthStore(t.TempDir(), testEncryptionKey())
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
	store, _ := OpenOAuthStore(t.TempDir(), testEncryptionKey())
	provider := newOAuthProvider(ServerConfig{Name: "railway", URL: "https://resource.example"}, store, http.DefaultClient)
	response := &http.Response{Body: io.NopCloser(strings.NewReader("unauthorized"))}
	if err := provider.Authorize(context.Background(), nil, response); err != ErrLoginRequired {
		t.Fatalf("error=%v", err)
	}
}

func TestOAuthProviderRejectsMismatchedState(t *testing.T) {
	store, _ := OpenOAuthStore(t.TempDir(), testEncryptionKey())
	provider := newOAuthProvider(ServerConfig{Name: "railway", URL: "https://resource.example", RedirectURL: "https://eggy.example/auth/mcp/railway/callback"}, store, &http.Client{Transport: &oauthRoundTripper{}})
	if _, err := provider.BeginLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := provider.CompleteLogin(context.Background(), "code", "wrong-state"); err == nil {
		t.Fatal("mismatched OAuth state accepted")
	}
}

func TestOAuthProviderPersistsRotatedRefreshToken(t *testing.T) {
	store, _ := OpenOAuthStore(t.TempDir(), testEncryptionKey())
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
