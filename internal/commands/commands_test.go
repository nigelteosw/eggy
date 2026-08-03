package commands

import (
	"context"
	"strings"
	"testing"
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
func TestOnlyEightTelegramCommandsAreAdvertised(t *testing.T) {
	got := TelegramAutocomplete()
	if len(got) != 8 {
		t.Fatalf("commands=%v", got)
	}
	want := []string{"help", "status", "stop", "clear", "model", "mcp", "google", "auto"}
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
	auto  bool
	saved []bool
}

func (g *fakeApprovalGate) AutoApprove(context.Context) (bool, error) { return g.auto, nil }

func (g *fakeApprovalGate) SetAutoApprove(_ context.Context, auto bool) error {
	g.auto = auto
	g.saved = append(g.saved, auto)
	return nil
}

// /auto takes no argument: it is a toggle an owner fires from a phone, and the
// reply names the resulting mode so the tap is confirmed rather than assumed.
func TestAutoCommandTogglesAndNamesTheResultingMode(t *testing.T) {
	gate := &fakeApprovalGate{}
	service := New(Options{Approvals: gate})
	output, handled, err := service.Execute(context.Background(), "/auto")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !gate.auto || !strings.HasPrefix(output, "Auto mode enabled.") {
		t.Fatalf("auto=%v output=%q", gate.auto, output)
	}
	output, _, err = service.Execute(context.Background(), "/auto")
	if err != nil {
		t.Fatal(err)
	}
	if gate.auto || !strings.HasPrefix(output, "Auto mode disabled.") {
		t.Fatalf("auto=%v output=%q", gate.auto, output)
	}
	if len(gate.saved) != 2 {
		t.Fatalf("expected two writes, got %v", gate.saved)
	}
}

// A bypass the owner switched on and forgot is the failure mode worth a line
// in /status. The safe state does not get one.
func TestStatusReportsAutoModeOnlyWhenEnabled(t *testing.T) {
	gate := &fakeApprovalGate{}
	service := New(Options{Approvals: gate})
	output, _, err := service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "Auto mode") {
		t.Fatalf("status mentioned auto mode while it was off: %q", output)
	}
	gate.auto = true
	output, _, err = service.Execute(context.Background(), "/status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Auto mode is enabled") {
		t.Fatalf("status did not report an enabled bypass: %q", output)
	}
}
