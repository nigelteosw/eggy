package mcp

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type clientSession interface {
	ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
	Close() error
}

type connector func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error)

func connectSDK(ctx context.Context, cfg ServerConfig, httpClient *http.Client, handler auth.OAuthHandler, opts *sdk.ClientOptions) (clientSession, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "eggy", Version: "1"}, opts)
	transport := &sdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient, OAuthHandler: handler}
	return client.Connect(ctx, transport, nil)
}
