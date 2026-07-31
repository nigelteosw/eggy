package mcp

import (
	"net/http"
	"time"
)

type ToolFilter struct {
	Include []string
	Exclude []string
}

type ServerConfig struct {
	Name string
	// Transport selects how the server is reached: TransportHTTP uses URL,
	// TransportStdio spawns Command with Args. An empty value means
	// TransportHTTP, so existing configurations keep working.
	Transport string
	URL       string
	Command   string
	Args      []string
	// EnvAllowlist names the environment variables a stdio child receives.
	// Anything unnamed is withheld, so Eggy's credentials do not leak into a
	// subprocess that has no business with them.
	EnvAllowlist []string
	RedirectURL  string
	Auth         string
	BearerToken  string
	// OAuthClientID and OAuthClientSecret are a client the owner registered by
	// hand, for an authorization server with no dynamic client registration.
	// An empty client ID means registration is attempted instead; an empty
	// secret with a set ID is a public client, authorized by PKCE alone.
	OAuthClientID             string
	OAuthClientSecret         string
	OAuthScopes               []string
	Enabled                   bool
	ConnectTimeout            time.Duration
	Timeout                   time.Duration
	MaxOutputBytes            int64
	SupportsParallelToolCalls bool
	// FailureThreshold and Cooldown are the failure policy applied per tool:
	// this many consecutive failures of one tool put that tool, and only that
	// tool, out of service for this long.
	FailureThreshold int
	Cooldown         time.Duration
	Filter           ToolFilter
}

func (c ServerConfig) withDefaults() ServerConfig {
	if c.Transport == "" {
		c.Transport = TransportHTTP
	}
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 3
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 30 * time.Second
	}
	return c
}

type Options struct {
	HTTPClient *http.Client
	Connect    connector
	Now        func() time.Time
	OAuthStore *OAuthStore
}
