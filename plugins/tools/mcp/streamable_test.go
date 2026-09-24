package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStreamableTransportFollowsToolListChanges runs the real SDK on both ends
// of the wire. Under the 2026-07-28 protocol a stateless server has no session
// and no GET stream: a list change reaches the client only through the
// subscriptions/listen stream the SDK opens because Eggy registers a
// ToolListChangedHandler. A server still keeping sessions negotiates down to
// 2025-11-25 and must keep working too. The fake-session tests call the handler
// directly, so only a real server shows the notification still arrives.
func TestStreamableTransportFollowsToolListChanges(t *testing.T) {
	for _, stateless := range []bool{true, false} {
		name := "stateful"
		if stateless {
			name = "stateless"
		}
		t.Run(name, func(t *testing.T) { testStreamableToolListChanges(t, stateless) })
	}
}

func testStreamableToolListChanges(t *testing.T, stateless bool) {
	server := sdk.NewServer(&sdk.Implementation{Name: "remote", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "first", InputSchema: objectSchema()}, echoHandler("first"))
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: stateless})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	manager, err := NewManager(context.Background(), []ServerConfig{{
		Name: "remote", URL: httpServer.URL, Enabled: true,
		ConnectTimeout: 5 * time.Second, Timeout: 5 * time.Second, MaxOutputBytes: 4096,
	}}, Options{HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if names := managerToolNames(manager); !slices.Equal(names, []string{"remote__first"}) {
		status, _ := manager.Status("remote")
		t.Fatalf("tools=%v status=%#v", names, status)
	}

	server.AddTool(&sdk.Tool{Name: "second", InputSchema: objectSchema()}, echoHandler("second"))
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(managerToolNames(manager), []string{"remote__first", "remote__second"}) {
		if time.Now().After(deadline) {
			t.Fatalf("list change never reached the catalog: tools=%v", managerToolNames(manager))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func echoHandler(text string) sdk.ToolHandler {
	return func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil
	}
}
