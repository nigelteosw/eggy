package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

func modelToolCall(t *testing.T, tool ports.Tool, arguments string) (string, error) {
	t.Helper()
	output, err := tool.Execute(context.Background(), json.RawMessage(arguments))
	return string(output), err
}

func openRouterCatalog() *fakeDiscovery {
	return &fakeDiscovery{
		providers: []string{"openrouter"},
		models: []ports.CatalogModel{
			{ID: "openai/gpt-5", Name: "OpenAI: GPT-5"},
			{ID: "google/gemini-2.5-flash-image", Name: "Google: Gemini 2.5 Flash Image", OutputModalities: []ports.Modality{ports.ModalityImage, "text"}},
			{ID: "anthropic/claude-sonnet-5", Name: "Anthropic: Claude Sonnet 5", Reasoning: &ports.CatalogReasoning{Efforts: []string{"low", "high"}}},
		},
	}
}

// The owner names a model loosely; search must find the exact ID, say that it
// draws, and say which catalog entry is already an alias.
func TestModelToolSearchFindsALooselyNamedModel(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	output, err := modelToolCall(t, service.ModelTool(), `{"action":"search","query":"Gemini flash IMAGE"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"id":"google/gemini-2.5-flash-image"`) || !strings.Contains(output, `"draws_images":true`) || strings.Contains(output, "gpt-5") {
		t.Fatalf("output=%s", output)
	}
	configured, _ := modelToolCall(t, service.ModelTool(), `{"action":"search","query":"gpt"}`)
	if !strings.Contains(configured, `"alias":"fast"`) {
		t.Fatalf("configured alias not reported: %s", configured)
	}
}

func TestModelToolAddWritesTheCatalogIDAndRestarts(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	restarter := &fakeRestarter{}
	service.Restarter = restarter
	service.Getenv = func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "test-key"
		}
		return ""
	}
	output, err := modelToolCall(t, service.ModelTool(), `{"action":"add","alias":"sonnet","model":"anthropic/claude-sonnet-5"}`)
	if err != nil {
		t.Fatal(err)
	}
	if restarter.restarts != 1 || !strings.Contains(output, "restarting") {
		t.Fatalf("restarts=%d output=%s", restarter.restarts, output)
	}
	body, _ := os.ReadFile(path)
	// The efforts come from the catalog, so the owner never has to know them.
	for _, want := range []string{"sonnet:", "anthropic/claude-sonnet-5", "- low", "- high"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("config missing %q:\n%s", want, body)
		}
	}
}

// A guessed ID is refused rather than written, and an alias is never
// silently replaced.
func TestModelToolAddRefusesGuessesAndTakenAliases(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	restarter := &fakeRestarter{}
	service.Restarter = restarter
	before, _ := os.ReadFile(path)
	if _, err := modelToolCall(t, service.ModelTool(), `{"action":"add","alias":"gem","model":"gemini-flash-image"}`); err == nil || !strings.Contains(err.Error(), "search") {
		t.Fatalf("guessed ID accepted: %v", err)
	}
	if _, err := modelToolCall(t, service.ModelTool(), `{"action":"add","alias":"fast","model":"anthropic/claude-sonnet-5"}`); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("taken alias accepted: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || restarter.restarts != 0 {
		t.Fatalf("a refused add changed something: restarts=%d", restarter.restarts)
	}
}

func TestModelToolSelectSwitchesAndExplainsAPendingRestart(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast", "plain"})
	models := service.AgentRuntime.(*fakeModels)
	if _, err := modelToolCall(t, service.ModelTool(), `{"action":"select","alias":"plain"}`); err != nil || models.selected != "plain" {
		t.Fatalf("selected=%q err=%v", models.selected, err)
	}
	if _, err := modelToolCall(t, service.ModelTool(), `{"action":"select","alias":"default"}`); err != nil || models.selected != "" {
		t.Fatalf("default did not clear: selected=%q err=%v", models.selected, err)
	}

	// An alias on disk but not yet loaded is reported as such.
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), "runner:", "  later:\n    provider: \"openrouter\"\n    model: \"openai/gpt-5\"\nrunner:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	service.AgentRuntime = rejectingModels{&fakeModels{selected: "fast"}}
	if _, err := modelToolCall(t, service.ModelTool(), `{"action":"select","alias":"later"}`); err == nil || !strings.Contains(err.Error(), "/restart") {
		t.Fatalf("pending alias err=%v", err)
	}
	listed, err := modelToolCall(t, New(service.Options).ModelTool(), `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, `"alias":"later"`) || !strings.Contains(listed, `"restart_pending":true`) {
		t.Fatalf("list=%s", listed)
	}
}

// Normal mode asks only before the shared config is written.
func TestModelToolGatesOnlyAdd(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	rule := services.RuleFor(service.ModelTool().Definition())
	for action, gated := range map[string]bool{"list": false, "search": false, "select": false, "add": true} {
		if got := rule.Gated(json.RawMessage(`{"action":"` + action + `"}`)); got != gated {
			t.Fatalf("%s gated=%v", action, got)
		}
	}
}

// rejectingModels refuses every alias, as the runtime does one it was not
// built with.
type rejectingModels struct{ *fakeModels }

func (rejectingModels) SelectModel(_ context.Context, alias string) error {
	return errors.New("model alias " + alias + " is not configured")
}
