package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type fakeTurns struct{ stopped bool }

func (f *fakeTurns) Stop(context.Context) bool {
	f.stopped = true
	return true
}

type fakeModels struct{ selected string }

func (f *fakeModels) SelectedModel(context.Context) (string, error) { return f.selected, nil }
func (f *fakeModels) SelectModel(_ context.Context, alias string) error {
	f.selected = alias
	return nil
}

// The command surface stays small and enumerated. /mcp and /google are the
// administration commands, and they are here because what they reach lives on
// the Eggy runtime -- an owner on a phone has no other way to authorize a
// server or a Google grant.
func TestOnlyNineTelegramCommandsAreAdvertised(t *testing.T) {
	got := TelegramAutocomplete()
	if len(got) != 9 {
		t.Fatalf("commands=%v", got)
	}
	want := []string{"help", "status", "stop", "clear", "model", "mcp", "google", "mode", "restart"}
	for index := range want {
		if got[index].Name != want[index] {
			t.Fatalf("commands=%v", got)
		}
	}
}

func TestUnknownSlashCommandGetsHelpAndProseFallsThrough(t *testing.T) {
	service := New(Options{})
	if output, handled, err := service.Execute(context.Background(), "hello"); err != nil || handled || output != "" {
		t.Fatalf("prose output=%q handled=%v err=%v", output, handled, err)
	}
	output, handled, err := service.Execute(context.Background(), "/repositories")
	if err != nil || !handled || !strings.Contains(output, "/help") {
		t.Fatalf("command output=%q handled=%v err=%v", output, handled, err)
	}
}

func TestStopAndModelDispatchDirectly(t *testing.T) {
	turns := &fakeTurns{}
	models := &fakeModels{selected: "fast"}
	service := New(Options{Turns: turns, AgentRuntime: models, DefaultModel: "fast", ModelAliases: []string{"fast", "smart"}})
	if _, handled, err := service.Execute(context.Background(), "/stop"); err != nil || !handled || !turns.stopped {
		t.Fatalf("handled=%v stopped=%v err=%v", handled, turns.stopped, err)
	}
	if output, handled, err := service.Execute(context.Background(), "/model smart"); err != nil || !handled || output != "Model set to smart." || models.selected != "smart" {
		t.Fatalf("output=%q handled=%v selected=%q err=%v", output, handled, models.selected, err)
	}
}

type fakeApprovalGate struct {
	mode  ports.ApprovalMode
	saved []ports.ApprovalMode
}

func (g *fakeApprovalGate) Mode(context.Context) (ports.ApprovalMode, error) {
	if g.mode == "" {
		return ports.ModeNormal, nil
	}
	return g.mode, nil
}

func (g *fakeApprovalGate) SetMode(_ context.Context, mode ports.ApprovalMode) error {
	g.mode = mode
	g.saved = append(g.saved, mode)
	return nil
}

// Bare /mode reports without changing anything. With three modes, a toggle
// that advanced to whichever came next would be a way to reach auto without
// having asked for it.
func TestModeCommandReportsAndSets(t *testing.T) {
	gate := &fakeApprovalGate{}
	service := New(Options{Approvals: gate})
	output, handled, err := service.Execute(context.Background(), "/mode")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.HasPrefix(output, "Normal mode.") || len(gate.saved) != 0 {
		t.Fatalf("a bare /mode changed something: output=%q saved=%v", output, gate.saved)
	}

	output, _, err = service.Execute(context.Background(), "/mode strict")
	if err != nil {
		t.Fatal(err)
	}
	if gate.mode != ports.ModeStrict || !strings.HasPrefix(output, "Strict mode.") {
		t.Fatalf("mode=%q output=%q", gate.mode, output)
	}

	// An unrecognized mode changes nothing and says what the three are, rather
	// than falling back to one of them.
	output, _, err = service.Execute(context.Background(), "/mode readonly")
	if err != nil {
		t.Fatal(err)
	}
	if gate.mode != ports.ModeStrict || !strings.Contains(output, "/mode strict") {
		t.Fatalf("mode=%q output=%q", gate.mode, output)
	}
	if len(gate.saved) != 1 {
		t.Fatalf("expected one write, got %v", gate.saved)
	}
}

type fakeRestarter struct{ restarts int }

func (f *fakeRestarter) Restart() { f.restarts++ }

func restartEnv(key string) string {
	if key == "DEEPSEEK_API_KEY" {
		return "test-key"
	}
	return ""
}

// /restart is the chat-side answer to "restart Eggy for this to take effect",
// so it must actually reach the supervisor rather than only saying it did.
func TestRestartCommandAsksTheDaemonToRebuild(t *testing.T) {
	restarter := &fakeRestarter{}
	service := New(Options{ConfigPath: mcpTestConfig(t), Restarter: restarter, Getenv: restartEnv})
	output := run(t, service, "/restart")
	if restarter.restarts != 1 {
		t.Fatalf("restarts=%d", restarter.restarts)
	}
	if !strings.Contains(output, "Restarting") {
		t.Fatalf("output=%q", output)
	}
}

// The pre-flight is the point of the command being safe to fire from a phone:
// a config that would not load sends the daemon to safe mode, where Telegram
// is gone. Refusing keeps the working Eggy and reports why.
func TestRestartRefusesAConfigThatWouldNotLoad(t *testing.T) {
	restarter := &fakeRestarter{}
	path := mcpTestConfig(t)
	if err := os.WriteFile(path, []byte("agent:\n  default_model: \"missing\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Options{ConfigPath: path, Restarter: restarter, Getenv: restartEnv})
	output := run(t, service, "/restart")
	if restarter.restarts != 0 {
		t.Fatalf("restarted into a broken config: restarts=%d", restarter.restarts)
	}
	if !strings.Contains(output, "Not restarting") {
		t.Fatalf("output=%q", output)
	}
}

// With three modes, silence no longer identifies one state, so /status names
// whichever is in force -- and still calls out the bypass as the one worth
// noticing.
func TestStatusReportsTheApprovalMode(t *testing.T) {
	gate := &fakeApprovalGate{}
	service := New(Options{Approvals: gate})
	output, _, err := service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Normal mode") {
		t.Fatalf("status did not name the mode: %q", output)
	}
	gate.mode = ports.ModeAuto
	output, _, err = service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Auto mode") || !strings.Contains(output, "/mode normal") {
		t.Fatalf("status did not report the bypass or the way out: %q", output)
	}
}

type fakeDiscovery struct {
	providers []string
	models    []ports.CatalogModel
	err       error
	asked     string
}

func (d *fakeDiscovery) DiscoverableProviders() []string { return d.providers }

func (d *fakeDiscovery) DiscoverModels(_ context.Context, provider string) ([]ports.CatalogModel, error) {
	d.asked = provider
	return d.models, d.err
}

func modelBrowseService(t *testing.T, discovery ModelDiscoverer, aliases []string) (*CommandService, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(browsableConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	return New(Options{
		ConfigPath: path, AgentRuntime: &fakeModels{selected: "fast"}, DefaultModel: "fast",
		ModelAliases: aliases, ModelDiscovery: discovery,
	}), path
}

func TestModelAvailableListsAndFiltersACatalog(t *testing.T) {
	discovery := &fakeDiscovery{
		providers: []string{"openrouter"},
		models: []ports.CatalogModel{
			{ID: "anthropic/claude-sonnet-5"}, {ID: "openai/gpt-5"}, {ID: "meta-llama/llama-4"},
		},
	}
	service, _ := modelBrowseService(t, discovery, []string{"fast"})

	output, handled, err := service.Execute(context.Background(), "/model available openrouter")
	if err != nil || !handled || discovery.asked != "openrouter" {
		t.Fatalf("output=%q handled=%v asked=%q err=%v", output, handled, discovery.asked, err)
	}
	for _, want := range []string{"anthropic/claude-sonnet-5", "openai/gpt-5", "/model add"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}

	filtered, _, err := service.Execute(context.Background(), "/model available openrouter gpt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filtered, "openai/gpt-5") || strings.Contains(filtered, "claude-sonnet-5") {
		t.Fatalf("filtered output:\n%s", filtered)
	}
}

// Browsing must never look like selecting: a provider that will not answer is
// a credential or an outage, and the owner needs to be told which.
func TestModelAvailableReportsProviderFailure(t *testing.T) {
	service, _ := modelBrowseService(t, &fakeDiscovery{providers: []string{"openrouter"}, err: errors.New("provider authentication failed (HTTP 401)")}, []string{"fast"})
	output, _, err := service.Execute(context.Background(), "/model available openrouter")
	if err != nil || !strings.Contains(output, "authentication failed") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestModelAddWritesAnAliasAndAsksForARestart(t *testing.T) {
	service, path := modelBrowseService(t, &fakeDiscovery{providers: []string{"openrouter"}}, []string{"fast"})
	output, handled, err := service.Execute(context.Background(), "/model add sonnet openrouter anthropic/claude-sonnet-5")
	if err != nil || !handled {
		t.Fatalf("output=%q handled=%v err=%v", output, handled, err)
	}
	if !strings.Contains(output, "/restart") {
		t.Fatalf("output must point at /restart:\n%s", output)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "anthropic/claude-sonnet-5") {
		t.Fatalf("alias was not written:\n%s", body)
	}
	// A rejected write must say so rather than report success.
	rejected, _, err := service.Execute(context.Background(), "/model add nope missing anthropic/claude-sonnet-5")
	if err != nil || !strings.Contains(rejected, "Could not add nope") {
		t.Fatalf("output=%q err=%v", rejected, err)
	}
}

// The subcommands are words an owner could plausibly have used as an alias.
// An existing alias must keep winning, or adding these names would quietly
// make somebody's model unselectable.
func TestModelSubcommandNeverShadowsAConfiguredAlias(t *testing.T) {
	models := &fakeModels{selected: "fast"}
	service := New(Options{
		AgentRuntime: models, DefaultModel: "fast", ModelAliases: []string{"fast", "available"},
		ModelDiscovery: &fakeDiscovery{providers: []string{"openrouter"}},
	})
	output, _, err := service.Execute(context.Background(), "/model available")
	if err != nil || models.selected != "available" {
		t.Fatalf("output=%q selected=%q err=%v", output, models.selected, err)
	}
}

func TestModelProvidersSaysWhichCanBeBrowsed(t *testing.T) {
	service, _ := modelBrowseService(t, &fakeDiscovery{providers: []string{"openrouter"}}, []string{"fast"})
	output, _, err := service.Execute(context.Background(), "/model providers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "openrouter (browsable") || !strings.Contains(output, "quiet (cannot be browsed)") {
		t.Fatalf("output:\n%s", output)
	}
}

func browsableConfig() string {
	return `server:
  listen: ":8080"
  public_base_url: "https://eggy.example"
data_dir: "./data"
owner:
  id: "12345"
agent:
  default_model: "fast"
  timezone: "UTC"
providers:
  openrouter:
    adapter: "openai_compatible"
    base_url: "https://openrouter.ai/api/v1"
    api_key_env: "OPENROUTER_API_KEY"
  quiet:
    adapter: "openai_compatible"
    base_url: "https://api.example/v1"
    api_key_env: "QUIET_API_KEY"
    discover_models: false
models:
  fast:
    provider: "openrouter"
    model: "openai/gpt-5"
runner:
  root: "./data/work"
`
}
