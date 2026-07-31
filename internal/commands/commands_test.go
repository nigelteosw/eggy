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

// The command surface stays small and enumerated. /mcp is the one
// administration command, and it is here because the config it edits lives on
// the Eggy runtime -- an owner on a phone has no other way to reach it.
func TestOnlySixTelegramCommandsAreAdvertised(t *testing.T) {
	got := TelegramAutocomplete()
	if len(got) != 6 {
		t.Fatalf("commands=%v", got)
	}
	want := []string{"help", "status", "stop", "clear", "model", "mcp"}
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
