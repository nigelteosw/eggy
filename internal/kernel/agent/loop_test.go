package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLoopSelectsAliasAndAccumulatesUsage(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}, Usage: ports.ModelUsage{PromptTokens: 3, TotalTokens: 3}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "ready"}, Usage: ports.ModelUsage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, 4)
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
	}, 4)
	if _, err := loop.RunSelected(context.Background(), "model", "heartbeat", nil, RunOptions{AllowedTools: map[string]bool{"status": true}}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "status" {
		t.Fatalf("tools=%#v", model.requests[0].Tools)
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
