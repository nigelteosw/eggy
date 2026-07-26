package bootstrap

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
)

func TestWebSearchToolIsAbsentWithoutEnvironment(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := app.loop.ToolNames(agent.RunOptions{}); slices.Contains(names, "web_search") {
		t.Fatalf("unconfigured tools=%v", names)
	}
}

func TestWebSearchToolIsRegisteredWhenConfigured(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.WebSearch = WebSearchConfig{
		Adapter: "searxng", BaseURLEnv: "WEB_SEARCH_API",
		Timeout: Duration(15 * time.Second), MaxResults: 8, SafeSearch: intPointer(1),
	}
	secrets := appTestSecrets("deepseek")
	secrets.WebSearchBaseURL = "https://search.example.com"
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	direct := app.loop.ToolNames(agent.RunOptions{})
	scheduled := app.loop.ToolNames(readOnlyRunOptions())
	heartbeat := app.loop.ToolNames(heartbeatRunOptions())
	if !slices.Contains(direct, "web_search") {
		t.Fatalf("direct tools=%v", direct)
	}
	if slices.Contains(scheduled, "web_search") || slices.Contains(heartbeat, "web_search") {
		t.Fatalf("scheduled=%v heartbeat=%v", scheduled, heartbeat)
	}
}

func TestFakeWebSearchRegistrationMakesNoNetworkCall(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.WebSearch = WebSearchConfig{
		Adapter: "searxng", BaseURLEnv: "WEB_SEARCH_API",
		Timeout: Duration(15 * time.Second), MaxResults: 8, SafeSearch: intPointer(1),
	}
	secrets := appTestSecrets("deepseek")
	secrets.WebSearchBaseURL = "https://search.example.com"
	client := &http.Client{Transport: appRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected network call")
	})}
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(app.loop.ToolNames(agent.RunOptions{}), "web_search") {
		t.Fatal("fake web search tool was not registered")
	}
}

func intPointer(value int) *int { return &value }
