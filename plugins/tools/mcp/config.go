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
	Name                      string
	URL                       string
	RedirectURL               string
	Auth                      string
	BearerToken               string
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
