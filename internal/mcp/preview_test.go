package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPreviewDisabledServerListsToolsWithoutRegistering(t *testing.T) {
	sessions := map[string]*fakeSession{
		"example": {tools: []*sdk.Tool{{Name: "read", InputSchema: objectSchema()}, {Name: "secret", InputSchema: objectSchema()}}},
	}
	manager, err := NewManager(context.Background(), []ServerConfig{
		{Name: "example", URL: "https://mcp.example", Enabled: false, Timeout: time.Second, MaxOutputBytes: 4096},
	}, Options{Connect: sessionConnector(sessions), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	previews, err := manager.Preview(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 2 || previews[0].Name != "read" || previews[1].Name != "secret" {
		t.Fatalf("previews = %+v", previews)
	}
	if len(manager.Tools()) != 0 {
		t.Fatal("preview registered tools on a disabled server")
	}
	if manager.Statuses()[0].State != StateDisabled {
		t.Fatalf("preview changed the server's state to %v", manager.Statuses()[0].State)
	}
}

func TestPreviewUnknownServerNeedsRestart(t *testing.T) {
	manager, err := NewManager(context.Background(), nil, Options{Connect: sessionConnector(nil), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Preview(context.Background(), "added-later"); !errors.Is(err, ErrRestartRequired) {
		t.Fatalf("err = %v, want ErrRestartRequired", err)
	}
}

func TestPreviewLoginRequired(t *testing.T) {
	connect := func(context.Context, ServerConfig, *http.Client, auth.OAuthHandler, *sdk.ClientOptions) (clientSession, error) {
		return nil, ErrLoginRequired
	}
	manager, err := NewManager(context.Background(), []ServerConfig{
		{Name: "example", URL: "https://mcp.example", Enabled: true, Auth: "none", Timeout: time.Second, MaxOutputBytes: 4096},
	}, Options{Connect: connect, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Preview(context.Background(), "example"); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
}
