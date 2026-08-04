package commands

import (
	"context"
	"os"
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
