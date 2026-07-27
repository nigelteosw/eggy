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

func googleCSEConfig(dataDir string) Config {
	cfg := appTestConfig(dataDir)
	cfg.WebSearch = WebSearchConfig{
		Adapter: "google_cse", APIKeyEnv: "GOOGLE_CSE_KEY", EngineIDEnv: "GOOGLE_CSE_ID",
		Timeout: Duration(15 * time.Second), MaxResults: 8, SafeSearch: intPointer(1),
	}
	return cfg
}

func TestGoogleCSEToolIsRegisteredWhenBothSecretsAreSet(t *testing.T) {
	secrets := appTestSecrets("deepseek")
	secrets.WebSearchAPIKey = "test-key"
	secrets.WebSearchEngineID = "test-cx"
	app, err := NewApp(googleCSEConfig(t.TempDir()), secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := app.loop.ToolNames(agent.RunOptions{}); !slices.Contains(names, "web_search") {
		t.Fatalf("direct tools=%v", names)
	}
}

// Google CSE needs both the key and the engine ID, so a half-configured
// deployment must start without web search rather than fail at startup.
func TestGoogleCSEToolIsAbsentWhenPartiallyConfigured(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		engineID string
	}{
		{name: "neither"},
		{name: "key only", apiKey: "test-key"},
		{name: "engine only", engineID: "test-cx"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			secrets := appTestSecrets("deepseek")
			secrets.WebSearchAPIKey = test.apiKey
			secrets.WebSearchEngineID = test.engineID
			app, err := NewApp(googleCSEConfig(t.TempDir()), secrets, AppOptions{FakeAdapters: true})
			if err != nil {
				t.Fatal(err)
			}
			if names := app.loop.ToolNames(agent.RunOptions{}); slices.Contains(names, "web_search") {
				t.Fatalf("tools=%v", names)
			}
		})
	}
}

// A SearXNG base URL must not enable the Google CSE adapter.
func TestGoogleCSEIgnoresSearXNGBaseURL(t *testing.T) {
	secrets := appTestSecrets("deepseek")
	secrets.WebSearchBaseURL = "https://search.example.com"
	app, err := NewApp(googleCSEConfig(t.TempDir()), secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := app.loop.ToolNames(agent.RunOptions{}); slices.Contains(names, "web_search") {
		t.Fatalf("tools=%v", names)
	}
}

func intPointer(value int) *int { return &value }

func TestTavilyToolIsRegisteredWithKeyAlone(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.WebSearch = WebSearchConfig{
		Adapter: "tavily", APIKeyEnv: "TAVILY_API_KEY", SearchDepth: "basic",
		Timeout: Duration(15 * time.Second), MaxResults: 8, SafeSearch: intPointer(1),
	}
	secrets := appTestSecrets("deepseek")
	secrets.WebSearchAPIKey = "test-key"
	app, err := NewApp(cfg, secrets, AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := app.loop.ToolNames(agent.RunOptions{}); !slices.Contains(names, "web_search") {
		t.Fatalf("direct tools=%v", names)
	}
}

func TestTavilyToolIsAbsentWithoutKey(t *testing.T) {
	cfg := appTestConfig(t.TempDir())
	cfg.WebSearch = WebSearchConfig{
		Adapter: "tavily", APIKeyEnv: "TAVILY_API_KEY", SearchDepth: "basic",
		Timeout: Duration(15 * time.Second), MaxResults: 8, SafeSearch: intPointer(1),
	}
	app, err := NewApp(cfg, appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if names := app.loop.ToolNames(agent.RunOptions{}); slices.Contains(names, "web_search") {
		t.Fatalf("tools=%v", names)
	}
}
