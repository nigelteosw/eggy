package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLoopSelectsAliasAndAccumulatesUsage(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, Usage: ports.ModelUsage{PromptTokens: 3, TotalTokens: 3}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, Usage: ports.ModelUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, StaticTools{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, ContextPolicy{})
	result, err := loop.Run(context.Background(), "deepseek-pro", "", ports.Message{Content: "status"}, nil, RunOptions{})
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
	if _, err := loop.Run(context.Background(), "missing", "", ports.Message{Content: "hello"}, nil, RunOptions{}); err == nil {
		t.Fatal("expected unknown alias error")
	}
}

func TestLoopCarriesImagePartsOnTheOwnerMessage(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Role: ports.RoleAssistant, Content: "seen"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, nil, ContextPolicy{})
	input := ports.Message{Content: "inspect", Parts: []ports.ContentPart{{Type: ports.ContentTypeImage, MediaType: "image/png", Data: []byte("png")}}}

	if _, err := loop.Run(context.Background(), "model", "", input, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	request := model.requests[0]
	got := request.Messages[len(request.Messages)-1]
	if got.Role != ports.RoleUser || got.Content != "inspect" || len(got.Parts) != 1 || string(got.Parts[0].Data) != "png" {
		t.Fatalf("owner message=%#v", got)
	}
}

func TestLoopSelectedCarriesReasoningContentFromTheFinalTurnOnly(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, ReasoningContent: "considering which tool to call"},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, ReasoningContent: "the tool confirmed readiness"},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, StaticTools{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, ContextPolicy{})
	result, err := loop.Run(context.Background(), "deepseek-pro", "", ports.Message{Content: "status"}, nil, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasoningContent != "the tool confirmed readiness" {
		t.Fatalf("reasoning content=%q, want the final turn's reasoning only", result.ReasoningContent)
	}
}

func TestLoopFiltersTools(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, ContextPolicy{})
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "heartbeat"}, nil, RunOptions{AllowedTools: map[string]bool{"status": true}}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "status" {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopOffersAllToolsWithoutAnAllowlist(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, ContextPolicy{})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "inspect"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests[0].Tools) != 2 {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopToolNamesMatchFilteredDefinitions(t *testing.T) {
	loop := NewSelectedLoop(nil, StaticTools{
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{tool}, ContextPolicy{})

	_, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "inspect"}, nil, RunOptions{AllowedTools: map[string]bool{}})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err=%v, want ErrUnknownTool", err)
	}
	if tool.calls != 0 {
		t.Fatalf("tool calls=%d, want 0", tool.calls)
	}
}

// A tool that covers a whole subject behind an "action" argument would
// otherwise be all-or-nothing on a restricted turn: granting a heartbeat the
// ability to read what is scheduled would also let it schedule things. A
// scoped grant admits the one action and refuses the rest.
func TestLoopScopedAllowlistEntryGrantsOneActionOfATool(t *testing.T) {
	allowed := map[string]bool{"schedule:list": true}
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "schedule", Arguments: json.RawMessage(`{"action":"list"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "nothing due"}},
	}}
	tool := &fakeTool{name: "schedule", result: json.RawMessage(`{"schedules":[]}`)}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{tool}, ContextPolicy{})

	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "heartbeat"}, nil, RunOptions{AllowedTools: allowed}); err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls=%d, want the granted action to run", tool.calls)
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "schedule" {
		t.Fatalf("tools=%#v, want the scoped tool offered", model.requests[0].Tools)
	}

	for _, arguments := range []string{`{"action":"cancel","id":"x"}`, `{}`, `not json`} {
		denied := &queuedModel{responses: []ports.ModelResponse{
			{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "schedule", Arguments: json.RawMessage(arguments)}}}},
		}}
		blocked := &fakeTool{name: "schedule"}
		loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: denied, ModelID: "id"}}, StaticTools{blocked}, ContextPolicy{})
		_, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "heartbeat"}, nil, RunOptions{AllowedTools: allowed})
		if !errors.Is(err, ErrUnknownTool) {
			t.Fatalf("arguments=%s err=%v, want ErrUnknownTool", arguments, err)
		}
		if blocked.calls != 0 {
			t.Fatalf("arguments=%s ran the tool anyway", arguments)
		}
	}
}

func TestLoopFiresToolStartBeforeEachToolExecutesAndNeverForTheFinalAnswer(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}},
	}}
	tool := &fakeTool{name: "status", result: json.RawMessage(`{}`)}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{tool}, ContextPolicy{})

	var calledBefore []int
	var calls []string
	_, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "status"}, nil, RunOptions{
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{propose}, ContextPolicy{})

	result, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "ship it"}, nil, RunOptions{})
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"hi"}`)},
	}, ContextPolicy{})
	var kinds []string
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "read"}, nil, RunOptions{
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{patch}, ContextPolicy{})

	var kinds []string
	result, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "edit"}, nil, RunOptions{
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
	if _, err := loop.Run(context.Background(), "missing", "", ports.Message{Content: "hello"}, nil, RunOptions{}); err == nil {
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"hi"}`)},
	}, ContextPolicy{})

	steered := []ports.Message{{Role: ports.RoleUser, Content: "actually, skip the tests"}}
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "have a look"}, nil, RunOptions{
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
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, StaticTools{
		&fakeTool{name: "read_file", result: json.RawMessage(`{}`)},
	}, ContextPolicy{})
	delivered := false
	if _, err := loop.Run(context.Background(), "model", "", ports.Message{Content: "go"}, nil, RunOptions{
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

// The tool source is read per turn, so a tool that appears after wiring is
// callable on the next turn without rebuilding the loop or restarting.
func TestLoopReadsItsToolSourcePerTurn(t *testing.T) {
	remote := &fakeTool{name: "railway__deploy", result: json.RawMessage(`{"ok":true}`)}
	var catalog []ports.Tool
	loop := NewSelectedLoop(map[string]ModelTarget{"pro": {Model: &queuedModel{}, ModelID: "provider-pro"}},
		ToolSourceFunc(func() []ports.Tool { return catalog }), ContextPolicy{})
	if names := loop.ToolNames(RunOptions{}); len(names) != 0 {
		t.Fatalf("names before the server connected=%v", names)
	}
	catalog = []ports.Tool{remote}
	if names := loop.ToolNames(RunOptions{}); !slices.Equal(names, []string{"railway__deploy"}) {
		t.Fatalf("names after the server connected=%v", names)
	}
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "railway__deploy", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "deployed"}},
	}}
	loop.selected["pro"] = ModelTarget{Model: model, ModelID: "provider-pro"}
	if _, err := loop.Run(context.Background(), "pro", "", ports.Message{Content: "deploy"}, nil, RunOptions{}); err != nil || remote.calls != 1 {
		t.Fatalf("calls=%d err=%v", remote.calls, err)
	}
	catalog = nil
	if names := loop.ToolNames(RunOptions{}); len(names) != 0 {
		t.Fatalf("names after logout=%v", names)
	}
}

// Precedence is settled by the source (services.ToolRegistry, which drops a
// provider tool colliding with a registered one). The loop keeps a first-wins
// backstop so a source that hands it duplicates anyway cannot substitute an
// impostor for a kernel primitive.
func TestLoopFirstToolWinsADuplicateNameFromItsSource(t *testing.T) {
	primitive := &fakeTool{name: "read_file", result: json.RawMessage(`{"source":"kernel"}`)}
	impostor := &fakeTool{name: "read_file", result: json.RawMessage(`{"source":"mcp"}`)}
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "read_file", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "read"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"pro": {Model: model, ModelID: "provider-pro"}},
		StaticTools{primitive, impostor}, ContextPolicy{})
	if names := loop.ToolNames(RunOptions{}); !slices.Equal(names, []string{"read_file"}) {
		t.Fatalf("names=%v", names)
	}
	if _, err := loop.Run(context.Background(), "pro", "", ports.Message{Content: "read"}, nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if primitive.calls != 1 || impostor.calls != 0 {
		t.Fatalf("primitive=%d impostor=%d", primitive.calls, impostor.calls)
	}
}
