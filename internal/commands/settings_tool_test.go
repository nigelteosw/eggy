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

func settingsCall(t *testing.T, tool ports.Tool, arguments string) (string, error) {
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
func TestSettingsToolSearchFindsALooselyNamedModel(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	output, err := settingsCall(t, service.SettingsTool(), `{"action":"search_models","query":"Gemini flash IMAGE"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"id":"google/gemini-2.5-flash-image"`) || !strings.Contains(output, `"draws_images":true`) || strings.Contains(output, "gpt-5") {
		t.Fatalf("output=%s", output)
	}
	configured, _ := settingsCall(t, service.SettingsTool(), `{"action":"search_models","query":"gpt"}`)
	if !strings.Contains(configured, `"alias":"fast"`) {
		t.Fatalf("configured alias not reported: %s", configured)
	}
}

func TestSettingsToolAddWritesTheCatalogIDAndRestarts(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	restarter := &fakeRestarter{}
	service.Restarter = restarter
	service.Getenv = func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "test-key"
		}
		return ""
	}
	output, err := settingsCall(t, service.SettingsTool(), `{"action":"add_model","alias":"sonnet","model":"anthropic/claude-sonnet-5"}`)
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
func TestSettingsToolAddRefusesGuessesAndTakenAliases(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	restarter := &fakeRestarter{}
	service.Restarter = restarter
	before, _ := os.ReadFile(path)
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"add_model","alias":"gem","model":"gemini-flash-image"}`); err == nil || !strings.Contains(err.Error(), "search") {
		t.Fatalf("guessed ID accepted: %v", err)
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"add_model","alias":"fast","model":"anthropic/claude-sonnet-5"}`); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("taken alias accepted: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || restarter.restarts != 0 {
		t.Fatalf("a refused add changed something: restarts=%d", restarter.restarts)
	}
}

func TestSettingsToolSelectSwitchesAndExplainsAPendingRestart(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast", "plain"})
	models := service.AgentRuntime.(*fakeModels)
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"select_model","alias":"plain"}`); err != nil || models.selected != "plain" {
		t.Fatalf("selected=%q err=%v", models.selected, err)
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"select_model","alias":"default"}`); err != nil || models.selected != "" {
		t.Fatalf("default did not clear: selected=%q err=%v", models.selected, err)
	}

	// An alias on disk but not yet loaded is reported as such.
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), "runner:", "  later:\n    provider: \"openrouter\"\n    model: \"openai/gpt-5\"\nrunner:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	service.AgentRuntime = rejectingModels{&fakeModels{selected: "fast"}}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"select_model","alias":"later"}`); err == nil || !strings.Contains(err.Error(), "/restart") {
		t.Fatalf("pending alias err=%v", err)
	}
	listed, err := settingsCall(t, New(service.Options).SettingsTool(), `{"action":"list_models"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, `"alias":"later"`) || !strings.Contains(listed, `"restart_pending":true`) {
		t.Fatalf("list=%s", listed)
	}
}

// Normal mode asks only before the shared config is written.
// How much the person is asked and whether Eggy may message them unprompted
// ask too: those are theirs to decide on a card, not a model's in passing.
func TestSettingsToolGatesSharedWritesAndConsent(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	rule := services.RuleFor(service.SettingsTool().Definition())
	for action, gated := range map[string]bool{
		"show": false, "list_models": false, "search_models": false, "select_model": false, "set_effort": false, "set_thinking": false,
		"show_theme": false, "set_theme": false,
		"add_model": true, "set_mode": true, "set_checkins": true, "set_checkin_schedule": true,
	} {
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

// show is how Eggy knows itself: this person's settings, the shared ones, and
// where each setting the tool cannot change is changed.
func TestSettingsToolShowDescribesEverySetting(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	service.Approvals = &fakeApprovalGate{mode: ports.ModeStrict}
	output, err := settingsCall(t, service.SettingsTool(), `{"action":"show"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model":"fast"`, `"thinking_shown":true`, `"check_ins":false`, "Every time", `"check_in_every":"off"`, `"timezone":"UTC"`, "Customize → Connectors", "/mode auto"} {
		if !strings.Contains(output, want) {
			t.Fatalf("show missing %q:\n%s", want, output)
		}
	}
}

// Nothing may choose auto on the person's behalf, and a tool call is exactly
// that; strict and normal go through the one approval authority.
func TestSettingsToolNeverSelectsAuto(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	gate := &fakeApprovalGate{}
	service.Approvals = gate
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_mode","mode":"auto"}`); err == nil || !strings.Contains(err.Error(), "only the person") {
		t.Fatalf("auto accepted: %v", err)
	}
	if output, err := settingsCall(t, service.SettingsTool(), `{"action":"set_mode","mode":"strict"}`); err != nil || gate.mode != ports.ModeStrict || !strings.Contains(output, "Every time") {
		t.Fatalf("mode=%q output=%s err=%v", gate.mode, output, err)
	}
	if len(gate.saved) != 1 {
		t.Fatalf("saved=%v", gate.saved)
	}
}

func TestSettingsToolSwitchesThisPersonsCheckins(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	models := service.AgentRuntime.(*fakeModels)
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_checkins","on":true}`); err != nil || !models.heartbeat {
		t.Fatalf("heartbeat=%v err=%v", models.heartbeat, err)
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_checkins","on":false}`); err != nil || models.heartbeat {
		t.Fatalf("heartbeat=%v err=%v", models.heartbeat, err)
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_checkins"}`); err == nil {
		t.Fatal("a call without on changed nothing but did not say so")
	}
}

// The panel's form, from chat: a field the call leaves out keeps its value,
// because config.SetHeartbeat replaces the interval and the hours whole.
func TestSettingsToolCheckinScheduleKeepsWhatItWasNotTold(t *testing.T) {
	service, path := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	restarter := &fakeRestarter{}
	service.Restarter = restarter
	service.Getenv = func(key string) string {
		return map[string]string{"OPENROUTER_API_KEY": "k", "QUIET_API_KEY": "k"}[key]
	}
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(string(body)+"telegram:\n  enabled: true\n  owner_id: 12345\nheartbeat:\n  interval: 3h\n  active_hours:\n    start: \"08:00\"\n    end: \"22:00\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	describe := service.SettingsTool().(ports.CallDescriber).DescribeCall(json.RawMessage(`{"action":"set_checkin_schedule","interval":"1h"}`))
	if !strings.Contains(describe, "every 1h") || !strings.Contains(describe, "everyone") {
		t.Fatalf("approval card=%q", describe)
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_checkin_schedule","interval":"1h"}`); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(path)
	for _, want := range []string{"interval: 1h", `start: "08:00"`, `end: "22:00"`} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("config missing %q:\n%s", want, saved)
		}
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_checkin_schedule","interval":"soon"}`); err == nil {
		t.Fatal("a non-duration interval was accepted")
	}
}

// A look is the person's own view of the panel, so the agent sets it unasked,
// through the same runtime the Appearance card uses.
func TestSettingsToolSetsThisPersonsLook(t *testing.T) {
	service, _ := modelBrowseService(t, openRouterCatalog(), []string{"fast"})
	models := service.AgentRuntime.(*fakeModels)
	output, err := settingsCall(t, service.SettingsTool(), `{"action":"set_theme","scheme":"dark","dark":{"primary":"150 60% 45%"},"text_font":"Inter"}`)
	if err != nil {
		t.Fatal(err)
	}
	if models.theme != "dark" || models.look.Custom == nil || models.look.Custom.Dark["primary"] != "150 60% 45%" || *models.look.TextFont != "Inter" {
		t.Fatalf("theme=%q look=%+v", models.theme, models.look)
	}
	if !strings.Contains(output, "custom, Inter text") {
		t.Fatalf("output=%s", output)
	}
	shown, err := settingsCall(t, service.SettingsTool(), `{"action":"show_theme"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"scheme":"dark"`, `"default_palettes"`, `"yolk-ink":"40 100% 18%"`} {
		if !strings.Contains(shown, want) {
			t.Fatalf("show_theme missing %q: %s", want, shown)
		}
	}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_theme"}`); err == nil {
		t.Fatal("an empty set_theme should be refused")
	}
	// A bad scheme refuses the whole request before the look is written.
	models.look = ports.LookChange{}
	if _, err := settingsCall(t, service.SettingsTool(), `{"action":"set_theme","scheme":"sepia","look":"cast-iron"}`); err == nil {
		t.Fatal("a bad scheme should be refused")
	}
	if models.look.Look != nil {
		t.Fatalf("a refused request stored the look: %+v", models.look)
	}
}
