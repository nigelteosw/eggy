package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLoopSelectsAliasAndAccumulatesUsage(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, Usage: ports.ModelUsage{PromptTokens: 3, TotalTokens: 3}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, Usage: ports.ModelUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, ContextPolicy{})
	result, err := loop.Run(context.Background(), "deepseek-pro", "", "status", nil, RunOptions{})
	if err != nil || result.Message.Content != "ready" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.Usage != (ports.ModelUsage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}) {
		t.Fatalf("usage=%#v", result.Usage)
	}
	for _, request := range model.requests {
		if request.Model != "provider-pro" {
			t.Fatalf("model=%q", request.Model)
		}
	}
	if _, err := loop.Run(context.Background(), "missing", "", "hello", nil, RunOptions{}); err == nil {
		t.Fatal("expected unknown alias error")
	}
}

func TestLoopSelectedCarriesReasoningContentFromTheFinalTurnOnly(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, ReasoningContent: "considering which tool to call"},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, ReasoningContent: "the tool confirmed readiness"},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, ContextPolicy{})
	result, err := loop.Run(context.Background(), "deepseek-pro", "", "status", nil, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningContent != "the tool confirmed readiness" {
		t.Fatalf("reasoning content=%q, want the final turn's reasoning only", result.ReasoningContent)
	}
}

func TestLoopFiltersTools(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, ContextPolicy{})
	if _, err := loop.Run(context.Background(), "model", "", "heartbeat", nil, RunOptions{AllowedTools: map[string]bool{"status": true}}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "status" {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopOffersAllToolsWithoutAnAllowlist(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, ContextPolicy{})

	if _, err := loop.Run(context.Background(), "model", "", "inspect", nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests[0].Tools) != 2 {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopToolNamesMatchFilteredDefinitions(t *testing.T) {
	loop := NewSelectedLoop(nil, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, ContextPolicy{})

	allNames := loop.ToolNames(RunOptions{})
	if len(allNames) != 2 || allNames[0] != "status" || allNames[1] != "repository_modify" {
		t.Fatalf("all names=%v", allNames)
	}
	allowedNames := loop.ToolNames(RunOptions{AllowedTools: map[string]bool{"status": true}})
	if len(allowedNames) != 1 || allowedNames[0] != "status" {
		t.Fatalf("allowed names=%v", allowedNames)
	}
}

func TestLoopRejectsToolCallExcludedByAllowlist(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "repository_modify"}}}}}}
	tool := &fakeTool{name: "repository_modify"}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{tool}, ContextPolicy{})

	_, err := loop.Run(context.Background(), "model", "", "inspect", nil, RunOptions{AllowedTools: map[string]bool{}})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err=%v, want ErrUnknownTool", err)
	}
	if tool.calls != 0 {
		t.Fatalf("tool calls=%d, want 0", tool.calls)
	}
}

func TestLoopFiresToolStartBeforeEachToolExecutesAndNeverForTheFinalAnswer(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}},
	}}
	tool := &fakeTool{name: "status", result: json.RawMessage(`{}`)}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{tool}, ContextPolicy{})

	var calledBefore []int
	var calls []string
	_, err := loop.Run(context.Background(), "model", "", "status", nil, RunOptions{
		OnEvent: func(event Event) {
			if event.Kind != EventToolStart {
				return
			}
			calls = append(calls, event.Call.Name)
			calledBefore = append(calledBefore, tool.calls)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "status" || calls[1] != "status" {
		t.Fatalf("calls=%v, want a tool_start event per tool call, never for the final answer", calls)
	}
	if calledBefore[0] != 0 || calledBefore[1] != 1 {
		t.Fatalf("calledBefore=%v, want tool_start to fire before the tool actually executes each time", calledBefore)
	}
}

// There is no terminal tool: a turn ends when the model stops calling
// tools, whether it spent that turn answering a question or shipping a
// change. propose_change is an ordinary tool whose result the model reads.
func TestLoopEndsWhenTheModelStopsCallingToolsNotWhenAToolIsCalled(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "propose_change", Arguments: json.RawMessage(`{"summary":"done"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "Opened https://example.test/pr/7"}},
	}}
	propose := &fakeTool{name: "propose_change", result: json.RawMessage(`{"pull_request_url":"https://example.test/pr/7"}`)}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{propose}, ContextPolicy{})

	result, err := loop.Run(context.Background(), "model", "", "ship it", nil, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "Opened https://example.test/pr/7" {
		t.Fatalf("message=%#v, want the turn to continue past the tool and report its result", result.Message)
	}
	if propose.calls != 1 {
		t.Fatalf("calls=%d, want 1", propose.calls)
	}
}

func TestLoopEmitsTheFullEventStreamForATurn(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "read it"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"hi"}`)},
	}, ContextPolicy{})
	var kinds []string
	if _, err := loop.Run(context.Background(), "model", "", "read", nil, RunOptions{
		OnEvent: func(event Event) { kinds = append(kinds, event.Kind) },
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(kinds, ","), "assistant_message,tool_start,tool_end"; got != want {
		t.Fatalf("event kinds=%s, want %s", got, want)
	}
}

// A failing tool is a result the model reacts to, not the end of the turn:
// this is why a rejected patch is recoverable without starting anything new.
func TestLoopHandsAToolFailureBackToTheModelAndContinues(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "patch", Arguments: json.RawMessage(`{"path":"main.go"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "patch", Arguments: json.RawMessage(`{"path":"main.go"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "fixed"}},
	}}
	patch := &sequencedTool{name: "patch", results: []json.RawMessage{nil, json.RawMessage(`{"status":"ok"}`)}, errs: []error{errors.New("old_string not found"), nil}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{patch}, ContextPolicy{})

	var kinds []string
	result, err := loop.Run(context.Background(), "model", "", "edit", nil, RunOptions{
		OnEvent: func(event Event) { kinds = append(kinds, event.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch.calls != 2 || result.Message.Content != "fixed" {
		t.Fatalf("calls=%d message=%#v", patch.calls, result.Message)
	}
	if !strings.Contains(strings.Join(kinds, ","), "tool_error") {
		t.Fatalf("kinds=%v, want the failure reported as an event", kinds)
	}
}

func TestLoopReportsUnknownModelAlias(t *testing.T) {
	loop := NewSelectedLoop(nil, nil, ContextPolicy{})
	if _, err := loop.Run(context.Background(), "missing", "", "hello", nil, RunOptions{}); err == nil {
		t.Fatal("expected unknown alias error")
	}
}

type sequencedTool struct {
	name    string
	results []json.RawMessage
	errs    []error
	calls   int
}

func (t *sequencedTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (t *sequencedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	i := t.calls
	t.calls++
	var result json.RawMessage
	var err error
	if i < len(t.results) {
		result = t.results[i]
	}
	if i < len(t.errs) {
		err = t.errs[i]
	}
	return result, err
}

type queuedModel struct {
	responses []ports.ModelResponse
	requests  []ports.ModelRequest
}

func (m *queuedModel) Generate(_ context.Context, request ports.ModelRequest) (ports.ModelResponse, error) {
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		return ports.ModelResponse{}, errors.New("no response queued")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

type fakeTool struct {
	name   string
	result json.RawMessage
	err    error
	calls  int
}

func (t *fakeTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (t *fakeTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	t.calls++
	return t.result, t.err
}

// Steering: a message that arrives mid-turn joins the messages the next model
// call sees, at the step boundary, rather than starting a competing turn.
func TestLoopAppendsSteeredInputAtEachStepBoundary(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ok, skipping the tests"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"hi"}`)},
	}, ContextPolicy{})

	steered := []ports.Message{{Role: ports.RoleUser, Content: "actually, skip the tests"}}
	if _, err := loop.Run(context.Background(), "model", "", "have a look", nil, RunOptions{
		PendingInput: func() []ports.Message {
			pending := steered
			steered = nil
			return pending
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("requests=%d", len(model.requests))
	}
	// The first call was already in flight, so the steer lands on the second.
	last := model.requests[1].Messages
	found := false
	for _, message := range last {
		if message.Role == ports.RoleUser && message.Content == "actually, skip the tests" {
			found = true
		}
	}
	if !found {
		t.Fatalf("steered message never reached the model: %#v", last)
	}
}

// Draining must not replay: a steered message appended once stays once, or a
// long turn would repeat the owner's instruction at every step.
func TestLoopDoesNotReplaySteeredInput(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{}`)},
	}, ContextPolicy{})
	delivered := false
	if _, err := loop.Run(context.Background(), "model", "", "go", nil, RunOptions{
		PendingInput: func() []ports.Message {
			if delivered {
				return nil
			}
			delivered = true
			return []ports.Message{{Role: ports.RoleUser, Content: "one more thing"}}
		},
	}); err != nil {
		t.Fatal(err)
	}
	occurrences := 0
	for _, message := range model.requests[len(model.requests)-1].Messages {
		if message.Content == "one more thing" {
			occurrences++
		}
	}
	if occurrences != 1 {
		t.Fatalf("steered message appears %d times, want exactly 1", occurrences)
	}
}
