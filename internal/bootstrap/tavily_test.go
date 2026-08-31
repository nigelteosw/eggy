package bootstrap

import (
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
)

// An unconfigured capability must cost nothing at all -- not a client, not a
// tool, and above all not two schemas on every model call for an owner who
// never wanted Eggy reaching the internet. That gate is the argument for
// shipping this as a core tool rather than an MCP server, so it is the one
// property worth a test of its own.
func TestTavilyDisabledRegistersNothing(t *testing.T) {
	tools := newTavilyTools(config.Config{}, config.Secrets{}, AppOptions{})
	if len(tools) != 0 {
		t.Fatalf("disabled Tavily built %d tools", len(tools))
	}
}

func TestTavilyEnabledRegistersSearchAndExtract(t *testing.T) {
	cfg := config.Config{Tavily: config.TavilyConfig{Enabled: true, APIKeyEnv: "TAVILY_API_KEY"}}
	tools := newTavilyTools(cfg, config.Secrets{TavilyAPIKey: "tvly-test"}, AppOptions{})
	if len(tools) != 2 {
		t.Fatalf("built %d tools, want web_search and web_extract", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		definition := tool.Definition()
		names[definition.Name] = true
		// ModeNormal lets these through on this claim, so it has to be true:
		// neither tool changes anything anywhere.
		if !definition.Effect.ReadOnly {
			t.Errorf("%s is not classified ReadOnly", definition.Name)
		}
	}
	if !names["web_search"] || !names["web_extract"] {
		t.Fatalf("registered %v", names)
	}
}
