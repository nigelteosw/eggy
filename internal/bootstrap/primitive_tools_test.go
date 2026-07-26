package bootstrap

import (
	"slices"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/kernel/services"
)

// TestPrimitiveToolsHaveExactlyOneDefinitionAcrossBothLoops is the guard on
// the unified tool surface: a primitive name must resolve to one definition
// no matter which loop is running, so a future adapter cannot reintroduce a
// shadowing read_file or terminal.
func TestPrimitiveToolsHaveExactlyOneDefinitionAcrossBothLoops(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range services.PrimitiveNames {
		conversational := count(app.loop.ToolNames(agent.RunOptions{}), name)
		implementation := count(app.implementationLoop.ToolNames(agent.RunOptions{}), name)
		if conversational != 1 {
			t.Fatalf("primitive %q appears %d times in the conversational tool surface, want exactly 1", name, conversational)
		}
		if implementation != 1 {
			t.Fatalf("primitive %q appears %d times in the implementation tool surface, want exactly 1", name, implementation)
		}
	}
}

// TestWritePrimitivesAreRegisteredForConversationalTurnsToo pins the "gate
// by result, not by registry membership" rule: patch and write_file are in
// the conversational tool list and refuse at execution time when the
// session's workspace is read-only.
func TestWritePrimitivesAreRegisteredForConversationalTurnsToo(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	names := app.loop.ToolNames(agent.RunOptions{})
	for _, name := range []string{"patch", "write_file"} {
		if !slices.Contains(names, name) {
			t.Fatalf("%q must stay registered for conversational turns; write gating is a result, not an absence. names=%v", name, names)
		}
	}
}

// TestFinishImplementationIsNotAConversationalTool keeps the run-terminal
// tool out of the ordinary chat surface: RunSelected ends when the model
// stops calling tools, so a terminal tool there means nothing.
func TestFinishImplementationIsNotAConversationalTool(t *testing.T) {
	app, err := NewApp(appTestConfig(t.TempDir()), appTestSecrets("deepseek"), AppOptions{FakeAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(app.loop.ToolNames(agent.RunOptions{}), "finish_implementation") {
		t.Fatal("finish_implementation must exist only in the implementation loop")
	}
	if !slices.Contains(app.implementationLoop.ToolNames(agent.RunOptions{}), "finish_implementation") {
		t.Fatal("the implementation loop needs its terminal tool")
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
