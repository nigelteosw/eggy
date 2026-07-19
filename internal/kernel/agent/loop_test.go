package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestLoopExecutesKnownToolAndReturnsFinalAnswer(t *testing.T) {
	flash := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "call-1", Name: "status", Arguments: json.RawMessage(`{"verbose":true}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "All systems ready."}},
	}}
	tool := &fakeTool{name: "status", result: json.RawMessage(`{"ready":true}`)}
	loop := NewLoop(flash, &queuedModel{}, []ports.Tool{tool}, Config{FlashModel: "flash", ProModel: "pro", MaxToolSteps: 4, EscalateAfterSteps: 3, EscalateAfterFailures: 2})
	message, err := loop.Run(context.Background(), "status please", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "All systems ready." || tool.calls != 1 {
		t.Fatalf("message=%#v calls=%d", message, tool.calls)
	}
	if len(flash.requests) != 2 || flash.requests[1].Messages[len(flash.requests[1].Messages)-1].Role != ports.RoleTool {
		t.Fatalf("requests=%#v", flash.requests)
	}
}

func TestLoopRejectsUnknownToolsAndStepOverflow(t *testing.T) {
	unknown := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "shell", Arguments: json.RawMessage(`{}`)}}}}}}
	loop := NewLoop(unknown, &queuedModel{}, nil, Config{FlashModel: "flash", ProModel: "pro", MaxToolSteps: 1})
	if _, err := loop.Run(context.Background(), "do it", nil, false); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("unknown tool error=%v", err)
	}

	repeating := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "2", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
	}}
	loop = NewLoop(repeating, &queuedModel{}, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, Config{FlashModel: "flash", ProModel: "pro", MaxToolSteps: 1, EscalateAfterSteps: 99})
	if _, err := loop.Run(context.Background(), "loop", nil, false); !errors.Is(err, ErrToolStepLimit) {
		t.Fatalf("step error=%v", err)
	}
}

func TestLoopEscalatesAtMostOnce(t *testing.T) {
	flash := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}}}}
	pro := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "2", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Content: "resolved by Pro"}},
	}}
	loop := NewLoop(flash, pro, []ports.Tool{&fakeTool{name: "status", result: json.RawMessage(`{}`)}}, Config{FlashModel: "flash", ProModel: "pro", MaxToolSteps: 4, EscalateAfterSteps: 1, EscalateAfterFailures: 2})
	message, err := loop.Run(context.Background(), "complex", nil, false)
	if err != nil || message.Content != "resolved by Pro" {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	if len(flash.requests) != 1 || len(pro.requests) != 2 {
		t.Fatalf("flash=%d pro=%d", len(flash.requests), len(pro.requests))
	}
	for _, request := range pro.requests {
		if request.Model != "pro" {
			t.Fatalf("model=%q", request.Model)
		}
	}
}

func TestLoopEscalatesAfterRecoverableToolFailuresAndSupportsExplicitPro(t *testing.T) {
	flash := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{ToolCalls: []ports.ToolCall{{ID: "2", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
	}}
	pro := &queuedModel{responses: []ports.ModelResponse{{Message: ports.Message{Content: "recovered"}}, {Message: ports.Message{Content: "explicit"}}}}
	tool := &fakeTool{name: "status", err: Recoverable(errors.New("temporary"))}
	loop := NewLoop(flash, pro, []ports.Tool{tool}, Config{FlashModel: "flash", ProModel: "pro", MaxToolSteps: 4, EscalateAfterSteps: 99, EscalateAfterFailures: 2})
	message, err := loop.Run(context.Background(), "status", nil, false)
	if err != nil || message.Content != "recovered" {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	message, err = loop.Run(context.Background(), "use pro", nil, true)
	if err != nil || message.Content != "explicit" {
		t.Fatalf("explicit message=%#v err=%v", message, err)
	}
}

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

func TestRouterRecognizesCodingAndComplexRequests(t *testing.T) {
	router := Router{Repositories: []string{"eggy"}, ComplexityLength: 80}
	if repo, ok := router.CodingIntent("Please fix the failing tests in eggy"); !ok || repo != "eggy" {
		t.Fatalf("repo=%q ok=%v", repo, ok)
	}
	if _, ok := router.CodingIntent("What is on my calendar?"); ok {
		t.Fatal("calendar query routed to coding")
	}
	if !router.ComplexNonCoding(strings.Repeat("analyze this tradeoff ", 10)) {
		t.Fatal("complex request not detected")
	}
}
