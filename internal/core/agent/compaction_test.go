package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

// scriptedTool answers with a fixed body per call, so a test can plant one
// early finding and then bury it under noise.
type scriptedTool struct {
	name    string
	replies []string
	calls   int
}

func (t *scriptedTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}

func (t *scriptedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	reply := ""
	if t.calls < len(t.replies) {
		reply = t.replies[t.calls]
	}
	t.calls++
	return json.RawMessage(reply), nil
}

// callStep is one scripted model turn that calls a tool.
func callStep(id, tool, arguments string) ports.ModelResponse {
	return ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{
		{ID: id, Name: tool, Arguments: json.RawMessage(arguments)},
	}}}
}

func lastRequest(t *testing.T, model *queuedModel) []ports.Message {
	t.Helper()
	if len(model.requests) == 0 {
		t.Fatal("model was never called")
	}
	return model.requests[len(model.requests)-1].Messages
}

func windowText(messages []ports.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
		for _, call := range message.ToolCalls {
			parts = append(parts, call.Name, string(call.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}

// A finding from an early step must survive compaction. "Used lookup" would
// tell the model it once looked something up while discarding the answer,
// which is the only reason the step happened.
func TestCompactionKeepsEarlyFindingsAfterFolding(t *testing.T) {
	tool := &scriptedTool{name: "lookup", replies: []string{`{"deploy_token":"tok-98217"}`}}
	responses := []ports.ModelResponse{callStep("1", "lookup", `{"key":"deploy_token"}`)}
	for i := 0; i < 8; i++ {
		responses = append(responses, callStep(fmt.Sprintf("n%d", i), "lookup", `{"key":"noise"}`))
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RecentSteps: 2})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "find the token"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	window := windowText(lastRequest(t, model))
	if !strings.Contains(window, "tok-98217") {
		t.Fatalf("the early finding was compacted away:\n%s", window)
	}
	if !strings.Contains(window, `"key":"deploy_token"`) {
		t.Fatalf("the arguments that produced the finding were lost:\n%s", window)
	}
	if !strings.Contains(window, "untrusted") {
		t.Fatalf("quoted tool output was not marked as untrusted data:\n%s", window)
	}
}

// A correction that arrives after the checkpoint is already full must still
// reach the model, and verbatim: a paraphrased correction is a correction
// lost, and a summary that stopped recording when full would drop exactly the
// newest instruction.
func TestCompactionPreservesSteeringAfterTheSummaryFills(t *testing.T) {
	filler := strings.Repeat("x", 4000)
	tool := &scriptedTool{name: "lookup"}
	responses := make([]ports.ModelResponse, 0, 20)
	for i := 0; i < 18; i++ {
		responses = append(responses, callStep(fmt.Sprintf("s%d", i), "lookup", `{}`))
		tool.replies = append(tool.replies, `{"body":"`+filler+`"}`)
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RecentSteps: 2})

	steps := 0
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "start"}, nil, RunOptions{
		PendingInput: func() []ports.Message {
			steps++
			if steps != 12 {
				return nil
			}
			return []ports.Message{{Role: ports.RoleUser, Content: "stop touching production"}}
		},
	}); err != nil {
		t.Fatal(err)
	}
	live := lastRequest(t, model)
	occurrences := strings.Count(windowText(live), "stop touching production")
	if occurrences != 1 {
		t.Fatalf("steering appears %d times, want exactly 1:\n%s", occurrences, windowText(live))
	}
	for _, message := range live {
		if message.Role == ports.RoleUser && message.Content == "stop touching production" {
			return
		}
	}
	t.Fatalf("the correction survived only as a summary line, not as the owner's message:\n%s", windowText(live))
}

// One oversized tool result must not blow the request budget, and must not
// vanish either: the checkpoint keeps a bounded excerpt that says how much it
// left out.
func TestCompactionBoundsAnOversizedToolResult(t *testing.T) {
	huge := strings.Repeat("y", 200000)
	tool := &scriptedTool{name: "read_file", replies: []string{`{"content":"` + huge + `"}`, `{"content":"small"}`}}
	model := &queuedModel{responses: []ports.ModelResponse{
		callStep("1", "read_file", `{"path":"big.log"}`),
		callStep("2", "read_file", `{"path":"small.log"}`),
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{tool}, ContextPolicy{RequestChars: 60000, ReserveChars: 4000, OutputExcerptChars: 512})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "read them"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	live := lastRequest(t, model)
	if chars := MessageChars(live); chars > 56000 {
		t.Fatalf("request carried %d characters against a 56000 allowance", chars)
	}
	window := windowText(live)
	if !strings.Contains(window, "characters shown") {
		t.Fatalf("the oversized result was dropped without saying so:\n%s", window)
	}
}

// Input the turn may not compact away is reported rather than sent: a request
// the provider will reject or silently truncate is worse than an error.
func TestCompactionReportsMandatoryInputThatCannotFit(t *testing.T) {
	history := []ports.Message{{Role: ports.RoleUser, Content: strings.Repeat("h", 40000)}}
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Role: ports.RoleAssistant, Content: "hi"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}},
		StaticTools{&scriptedTool{name: "lookup"}}, ContextPolicy{RequestChars: 20000, ReserveChars: 4000})

	_, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "go"}, history, RunOptions{})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err=%v, want ErrContextTooLarge", err)
	}
	if len(model.requests) != 0 {
		t.Fatalf("an unsendable request was sent anyway: %d calls", len(model.requests))
	}
}

// Images count against the budget by their encoded size. An input the budget
// cannot see is an input that evades it.
func TestMessageCharsCountsNonTextParts(t *testing.T) {
	message := ports.Message{Role: ports.RoleUser, Content: "look", Parts: []ports.ContentPart{{Data: make([]byte, 1024)}}}
	if chars := MessageChars([]ports.Message{message}); chars != 1028 {
		t.Fatalf("chars=%d, want 1028", chars)
	}
}

// The tool catalog is part of the request, so it is part of the budget.
func TestDefinitionCharsCountsTheCatalog(t *testing.T) {
	definitions := []ports.ToolDefinition{{Name: "ab", Description: "cd", Schema: json.RawMessage(`{}`)}}
	if chars := DefinitionChars(definitions); chars != 6 {
		t.Fatalf("chars=%d, want 6", chars)
	}
}

// A full summary drops its oldest entries, never the newest: the checkpoint
// exists to carry what the turn is working from right now.
func TestAppendSummaryKeepsTheNewestEntries(t *testing.T) {
	summary := ""
	for i := 0; i < 200; i++ {
		summary = AppendSummary(summary, fmt.Sprintf("entry %d %s", i, strings.Repeat("z", 500)))
	}
	if !strings.Contains(summary, "entry 199 ") {
		t.Fatal("the newest entry was dropped")
	}
	if strings.Contains(summary, "entry 0 ") {
		t.Fatal("the oldest entry was kept past the limit")
	}
}
