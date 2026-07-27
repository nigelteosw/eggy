package mcp

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewFakeManager(configs []ServerConfig) (*Manager, error) {
	catalogs := make(map[string]*fakeCatalogSession, len(configs))
	for index := range configs {
		cfg := &configs[index]
		cfg.Auth = ""
		if cfg.Timeout == 0 {
			cfg.Timeout = time.Second
		}
		if cfg.MaxOutputBytes == 0 {
			cfg.MaxOutputBytes = 4096
		}
		var tools []*sdk.Tool
		for _, name := range cfg.Filter.Include {
			tools = append(tools, &sdk.Tool{Name: name, Description: "Fake MCP tool", InputSchema: map[string]any{"type": "object", "additionalProperties": false}})
		}
		catalogs[cfg.Name] = &fakeCatalogSession{tools: tools}
	}
	connect := func(_ context.Context, cfg ServerConfig, _ *http.Client, _ auth.OAuthHandler, _ *sdk.ClientOptions) (clientSession, error) {
		return catalogs[cfg.Name], nil
	}
	return NewManager(context.Background(), configs, Options{Connect: connect, Now: time.Now})
}

type fakeCatalogSession struct {
	tools []*sdk.Tool
}

func (s *fakeCatalogSession) ListTools(context.Context, *sdk.ListToolsParams) (*sdk.ListToolsResult, error) {
	return &sdk.ListToolsResult{Tools: s.tools}, nil
}

func (s *fakeCatalogSession) CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "fake MCP result"}}, StructuredContent: map[string]any{}}, nil
}

func (s *fakeCatalogSession) Close() error { return nil }
