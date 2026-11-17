package bootstrap

import (
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/core/turns"
	"github.com/nigelteosw/eggy/internal/weather"
)

// Weather is part of Home, not an extension: the tool is always registered,
// is read-only so normal mode never asks, and is on the unprompted allowlist so
// a morning check-in can warn about rain.
func TestWeatherToolIsAlwaysRegisteredAndReachableUnprompted(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	direct := app.loop.ToolNames(agent.RunOptions{})
	scheduled := app.loop.ToolNames(turns.ReadOnlyTools())
	if !slices.Contains(direct, weather.ToolName) || !slices.Contains(scheduled, weather.ToolName) {
		t.Fatalf("direct=%v scheduled=%v", direct, scheduled)
	}
	for _, listing := range app.tools.Catalog() {
		if listing.Definition.Name == weather.ToolName && strings.Contains(listing.Definition.Description, "requires the owner's approval") {
			t.Fatal("the weather tool is gated in normal mode")
		}
	}
}
