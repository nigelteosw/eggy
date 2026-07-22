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
	Filter                    ToolFilter
}

type Options struct {
	HTTPClient *http.Client
	Connect    connector
	Now        func() time.Time
	OAuthStore *OAuthStore
}
