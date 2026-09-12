// Package google is the inbound half of Eggy's Google relationship: Google
// Sign-In, which answers "who is this person". It is deliberately separate
// from plugins/tools/google, the outbound half that holds Eggy's own
// Workspace grant and answers "what may Eggy do". The two never import each
// other: one verifies a person's ID token and keeps nothing, the other keeps
// a refresh token and never sees a person.
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Google's fixed endpoints. They are var rather than const only so the tests
// can point them at a fake provider; nothing an operator configures reaches
// them, and there is no discovery step whose document could redirect them.
var (
	issuerURL   = "https://accounts.google.com"
	authURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL    = "https://oauth2.googleapis.com/token"
	jwksURL     = "https://www.googleapis.com/oauth2/v3/certs"
	loginScopes = []string{oidc.ScopeOpenID, "email", "profile"}
)

// Identity is what a completed sign-in establishes about the person: the
// issuer/subject pair Google promises is stable for them, and the address
// they presented, with Google's word on whether it is theirs. Nothing else
// from the token is kept.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}

// Client performs one provider's authorization-code flow with PKCE and
// verifies the ID token it returns. It holds the Web application client's
// credentials and the callback URL registered for them; per-login secrets
// (state, nonce, verifier) are the caller's, kept in the login transaction.
type Client struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client
}

// New builds a client for the configured Web application OAuth client.
// redirectURL must be the exact callback registered with Google; a mismatch
// is refused by Google, not tolerated here.
func New(clientID, clientSecret, redirectURL string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, errors.New("google login client id and secret are required")
	}
	if strings.TrimSpace(redirectURL) == "" {
		return nil, errors.New("google login redirect url is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ctx := oidc.ClientContext(context.Background(), httpClient)
	keys := oidc.NewRemoteKeySet(ctx, jwksURL)
	verifier := oidc.NewVerifier(issuerURL, keys, &oidc.Config{ClientID: clientID})
	return &Client{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
			Endpoint: oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
			Scopes:   loginScopes,
		},
		verifier: verifier,
		http:     httpClient,
	}, nil
}

// Begin returns the URL to send the browser to. state, nonce and verifier are
// the caller's per-login secrets; only their derived forms travel.
func (c *Client) Begin(_ context.Context, state, nonce, verifier string) (string, error) {
	if state == "" || nonce == "" || verifier == "" {
		return "", errors.New("state, nonce and verifier are required")
	}
	return c.config.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// Complete exchanges the code and verifies the ID token: signature against
// Google's keys, issuer, audience, expiry, and the nonce this login was
// started with. The access token the exchange also returns is dropped: Eggy
// asked for identity, not access, and keeps nothing that could act as the
// person later.
func (c *Client) Complete(ctx context.Context, code, nonce, verifier string) (Identity, error) {
	if code == "" || nonce == "" || verifier == "" {
		return Identity{}, errors.New("code, nonce and verifier are required")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.http)
	token, err := c.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("exchange code: %w", err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errors.New("token response carried no id_token")
	}
	idToken, err := c.verifier.Verify(oidc.ClientContext(ctx, c.http), raw)
	if err != nil {
		return Identity{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return Identity{}, errors.New("id_token nonce does not match this login")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("read id_token claims: %w", err)
	}
	if idToken.Subject == "" {
		return Identity{}, errors.New("id_token carried no subject")
	}
	return Identity{
		Issuer:        idToken.Issuer,
		Subject:       idToken.Subject,
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: claims.EmailVerified,
	}, nil
}
