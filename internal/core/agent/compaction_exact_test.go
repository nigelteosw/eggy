package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// exactPolicy is the coding-run shape: folding waits for the request budget,
// folds in one batch down to FoldChars, and keeps excerpts usable for an edit.
func exactPolicy() ContextPolicy {
	return ContextPolicy{
		BudgetChars:        1 << 20,
		RecentSteps:        1000,
		OutputExcerptChars: 4096,
		RequestChars:       6000,
		ReserveChars:       500,
		FoldChars:          2500,
		PreserveExcerpts:   true,
	}
}

// A folded tool result keeps its newlines and indentation, so the model can
// copy exact text out of it. Collapsing whitespace made old text unmatchable.
func TestPreservedExcerptsKeepLinesAndIndentation(t *testing.T) {
	source := "func a() {\n\treturn 1\n}"
	tool := &scriptedTool{name: "read", replies: []string{source}}
	responses := []ports.ModelResponse{callStep("1", "read", `{"path":"a.go"}`)}
	for i := 0; i < 12; i++ {
		responses = append(responses, callStep(fmt.Sprintf("n%d", i), "read", `{"path":"noise"}`+strings.Repeat(" ", 600)))
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"m": {Model: model, ModelID: "id"}}, StaticTools{tool}, exactPolicy())

	if _, err := loop.Run(context.Background(), "m", "", ports.Message{Content: "go"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastCheckpoint(t, model), "\treturn 1") {
		t.Fatalf("folded excerpt lost its indentation:\n%s", lastCheckpoint(t, model))
	}
}

// The checkpoint names the files read and edited, and says plainly that its
// excerpts are not file contents, so the model re-reads before it edits.
func TestPreservedCheckpointListsFilesAndTellsModelToReread(t *testing.T) {
	read := &scriptedTool{name: "read", replies: []string{"x"}}
	edit := &scriptedTool{name: "edit", replies: []string{"edited"}}
	responses := []ports.ModelResponse{
		callStep("r", "read", `{"path":"internal/a.go"}`),
		callStep("e", "edit", `{"path":"internal/b.go","edits":[]}`),
	}
	for i := 0; i < 12; i++ {
		responses = append(responses, callStep(fmt.Sprintf("n%d", i), "read", `{"path":"noise"}`+strings.Repeat(" ", 600)))
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"m": {Model: model, ModelID: "id"}}, StaticTools{read, edit}, exactPolicy())

	if _, err := loop.Run(context.Background(), "m", "", ports.Message{Content: "go"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	checkpoint := lastCheckpoint(t, model)
	for _, want := range []string{"internal/a.go", "internal/b.go", "re-read"} {
		if !strings.Contains(checkpoint, want) {
			t.Fatalf("checkpoint missing %q:\n%s", want, checkpoint)
		}
	}
}

// Batch folding keeps the checkpoint stable: once folded, it is not rewritten
// on every call, so the cached prefix after it is reused between folds.
func TestBatchFoldingKeepsTheCheckpointStableBetweenFolds(t *testing.T) {
	// Sized so the checkpoint cap (SummaryLimit) fits well inside the request:
	// the test is about fold cadence, not about an undersized window.
	policy := ContextPolicy{
		BudgetChars:        1 << 20,
		RecentSteps:        1000,
		OutputExcerptChars: 4096,
		RequestChars:       60000,
		ReserveChars:       500,
		FoldChars:          30000,
		PreserveExcerpts:   true,
	}
	const steps = 120
	replies := make([]string, steps)
	for i := range replies {
		replies[i] = strings.Repeat("y", 800)
	}
	tool := &scriptedTool{name: "read", replies: replies}
	var responses []ports.ModelResponse
	for i := 0; i < steps; i++ {
		responses = append(responses, callStep(fmt.Sprintf("n%d", i), "read", `{"path":"f"}`))
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"m": {Model: model, ModelID: "id"}}, StaticTools{tool}, policy)

	if _, err := loop.Run(context.Background(), "m", "", ports.Message{Content: "go"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	changes := 0
	var previous string
	for _, request := range model.requests {
		checkpoint := checkpointOf(request.Messages)
		if checkpoint != "" && checkpoint != previous {
			changes++
		}
		previous = checkpoint
	}
	if changes == 0 {
		t.Fatal("no fold happened, so the test measured nothing")
	}
	// Folding one step per call would rewrite the checkpoint on nearly every
	// request after the first fold; batching rewrites it only a few times.
	if changes > 5 {
		t.Fatalf("checkpoint rewritten %d times over %d requests; batch folding should keep it stable", changes, len(model.requests))
	}
}

// A response cut off by the output limit may carry tool calls whose arguments
// were truncated. They are not run; the model is told to re-issue them.
func TestTruncatedResponseDoesNotRunItsToolCalls(t *testing.T) {
	tool := &scriptedTool{name: "edit", replies: []string{"should not run"}}
	cut := callStep("c1", "edit", `{"path":"a.go","edits":[{"old":"x`)
	cut.Truncated = true
	model := &queuedModel{responses: []ports.ModelResponse{cut, {Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"m": {Model: model, ModelID: "id"}}, StaticTools{tool}, ContextPolicy{})

	if _, err := loop.Run(context.Background(), "m", "", ports.Message{Content: "go"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if tool.calls != 0 {
		t.Fatalf("truncated tool call was executed %d times", tool.calls)
	}
	last := lastRequest(t, model)
	result := last[len(last)-1]
	if result.Role != ports.RoleTool || !strings.Contains(result.Content, "Re-issue") {
		t.Fatalf("model not told to re-issue the call: %+v", result)
	}
}

// The handoff a run carries to its next leg includes the live tail, not only
// the folded checkpoint, so the earlier leg's last steps are not lost.
func TestRunResultCarriesTheLiveTailAsHandoff(t *testing.T) {
	tool := &scriptedTool{name: "read", replies: []string{"the answer is 42"}}
	responses := []ports.ModelResponse{
		callStep("1", "read", `{"path":"last.go"}`),
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "asking"}},
	}
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"m": {Model: model, ModelID: "id"}}, StaticTools{tool}, exactPolicy())

	result, err := loop.Run(context.Background(), "m", "", ports.Message{Content: "go"}, nil, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Checkpoint, "the answer is 42") {
		t.Fatalf("handoff lost the live tail: %q", result.Checkpoint)
	}
}

func lastCheckpoint(t *testing.T, model *queuedModel) string {
	t.Helper()
	if len(model.requests) == 0 {
		t.Fatal("model was never called")
	}
	checkpoint := checkpointOf(model.requests[len(model.requests)-1].Messages)
	if checkpoint == "" {
		t.Fatal("no checkpoint was folded into the last request")
	}
	return checkpoint
}

func checkpointOf(messages []ports.Message) string {
	for _, message := range messages {
		if message.Role == ports.RoleSystem && strings.HasPrefix(message.Content, "Checkpoint of earlier work") {
			return message.Content
		}
	}
	return ""
}
