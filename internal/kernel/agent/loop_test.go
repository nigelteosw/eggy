package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/lane"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLoopSelectsAliasAndAccumulatesUsage(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, Usage: ports.ModelUsage{PromptTokens: 3, TotalTokens: 3}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, Usage: ports.ModelUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, nil, 4)
	result, err := loop.RunSelected(context.Background(), "deepseek-pro", "status", nil, RunOptions{})
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
	if _, err := loop.RunSelected(context.Background(), "missing", "hello", nil, RunOptions{}); err == nil {
		t.Fatal("expected unknown alias error")
	}
}

func TestLoopFiltersTools(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, nil, 4)
	if _, err := loop.RunSelected(context.Background(), "model", "heartbeat", nil, RunOptions{AllowedTools: map[string]bool{"status": true}}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "status" {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopFiltersImplementationToolsByLane(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, []string{"repository_modify"}, 4)

	if _, err := loop.RunSelected(context.Background(), "model", "inspect", nil, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "status" {
		t.Fatalf("assistant tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopAllowsImplementationToolsByLane(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "done"}}}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, []string{"repository_modify"}, 4)

	if _, err := loop.RunSelected(context.Background(), "model", "implement", nil, RunOptions{Lane: lane.Implementation}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests[0].Tools) != 2 {
		t.Fatalf("implementation tools=%#v", model.requests[0].Tools)
	}
}

func TestLoopToolNamesMatchFilteredDefinitions(t *testing.T) {
	loop := NewSelectedLoop(nil, []ports.Tool{
		&fakeTool{name: "status"}, &fakeTool{name: "repository_modify"},
	}, []string{"repository_modify"}, 4)

	assistantNames := loop.ToolNames(RunOptions{})
	if len(assistantNames) != 1 || assistantNames[0] != "status" {
		t.Fatalf("assistant names=%v", assistantNames)
	}
	implementationNames := loop.ToolNames(RunOptions{Lane: lane.Implementation})
	if len(implementationNames) != 2 || implementationNames[0] != "status" || implementationNames[1] != "repository_modify" {
		t.Fatalf("implementation names=%v", implementationNames)
	}
	allowedNames := loop.ToolNames(RunOptions{Lane: lane.Implementation, AllowedTools: map[string]bool{"status": true}})
	if len(allowedNames) != 1 || allowedNames[0] != "status" {
		t.Fatalf("allowed names=%v", allowedNames)
	}
}

func TestLoopRejectsImplementationToolCallOutsideImplementationLane(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "repository_modify"}}}}}}
	tool := &fakeTool{name: "repository_modify"}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{tool}, []string{"repository_modify"}, 4)

	_, err := loop.RunSelected(context.Background(), "model", "inspect", nil, RunOptions{})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err=%v, want ErrUnknownTool", err)
	}
	if tool.calls != 0 {
		t.Fatalf("tool calls=%d, want 0", tool.calls)
	}
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
