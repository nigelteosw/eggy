package agentrunner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Eggy reads a run's steps through agent_runner, so it can say what a run did
// and why it stopped without asking the owner to open the page. The step list
// is a read: it is not a mutation and needs no approval.
func TestAgentRunnerToolReportsRunSteps(t *testing.T) {
	h := newHarness(t, nil, func(int, ports.ModelRequest) (ports.ModelResponse, error) { return finalAnswer("changed README"), nil })
	run := prepared(t, h, startRun(t, h, "x"))
	if err := h.svc.execute(acct("a"), run); err != nil {
		t.Fatal(err)
	}
	tool := NewTool(h.svc)
	if strings.Contains(strings.Join(tool.Definition().Effect.Mutations, ","), "steps") {
		t.Fatal("steps is a read; it must not be declared as a mutation")
	}
	out, err := tool.Execute(acct("a"), json.RawMessage(`{"action":"steps","id":"`+run.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"kind":"started"`) || !strings.Contains(string(out), `"kind":"completed"`) {
		t.Fatalf("steps = %s, want the start and the outcome", out)
	}
}
