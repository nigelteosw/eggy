package bootstrap

import (
	"slices"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/services"
)

// TestPrimitiveToolsHaveExactlyOneDefinition is the guard on the unified
// tool surface. It used to say "across both loops"; there is one loop now,
// so the invariant is simply that a primitive name resolves once, and no
// adapter can reintroduce a shadowing read_file or terminal.
func TestPrimitiveToolsHaveExactlyOneDefinition(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range services.PrimitiveNames {
		if got := count(names, name); got != 1 {
			t.Fatalf("primitive %q appears %d times in the tool surface, want exactly 1", name, got)
		}
	}
}

// TestWritePrimitivesAreRegisteredForEveryTurn pins the "gate by result, not
// by registry membership" rule: patch and write_file are always in the tool
// list and refuse at execution time when the thread's workspace has no
// branch.
func TestWritePrimitivesAreRegisteredForEveryTurn(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range []string{"patch", "write_file"} {
		if !slices.Contains(names, name) {
			t.Fatalf("%q must stay registered; write gating is a result, not an absence. names=%v", name, names)
		}
	}
}

// The terminal tool is gone with the second loop: shipping is an action
// whose result the model reads, so nothing ends a turn but the model
// choosing to stop calling tools.
func TestShippingIsAnOrdinaryToolAndNoTerminalToolSurvives(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	if slices.Contains(names, "finish_implementation") {
		t.Fatal("finish_implementation must not exist: no tool terminates a turn")
	}
	for _, gone := range []string{"repository_modify", "repository_continue"} {
		if slices.Contains(names, gone) {
			t.Fatalf("%q must not exist: changing a repository is ordinary turns, not a nested run", gone)
		}
	}
	for _, name := range []string{"workspace_open", "workspace_edit", "propose_change", "workspace_close"} {
		if !slices.Contains(names, name) {
			t.Fatalf("%q must be registered. names=%v", name, names)
		}
	}
}

func count(names []string, want string) int {
	total := 0
	for _, name := range names {
		if name == want {
			total++
		}
	}
	return total
}
