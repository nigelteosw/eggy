package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestNativeImplementerReturnsStructuredResultAndReportsToolProgress(t *testing.T) {
	model := &sequencedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "terminal", Arguments: json.RawMessage(`{"command":"ls"}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "2", Name: "finish_implementation", Arguments: json.RawMessage(`{"summary":"done","commit_message":"feat: done","changed_files":["main.go"]}`)}}}},
	}}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: model, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	var updates []ports.CodingProgress
	result, err := implementer.Implement(context.Background(), ImplementationRequest{RunID: "run-1", Workspace: "/tmp/run-1", Instruction: "fix the bug"}, nil, func(p ports.CodingProgress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "done" || result.CommitMessage != "feat: done" || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "main.go" {
		t.Fatalf("result=%#v", result)
	}
	if len(updates) != 1 || updates[0].Kind != "milestone" || updates[0].RunID != "run-1" || updates[0].Message != "Ran: ls" {
		t.Fatalf("updates=%#v", updates)
	}
}

func TestImplementationProgressReportsNonZeroValidationExit(t *testing.T) {
	message := implementationProgressMessage(agent.ImplementationEvent{
		Kind:   "tool_end",
		Call:   ports.ToolCall{Name: "terminal", Arguments: json.RawMessage(`{"command":"go test ./..."}`)},
		Output: `{"exit_code":1}`,
	})
	if message != "Validation: go test ./... failed (exit 1)" {
		t.Fatalf("message=%q", message)
	}
}

func TestNativeImplementerRejectsConcurrentRunsWithSameID(t *testing.T) {
	block := &blockingModel{unblock: make(chan struct{}), started: make(chan struct{})}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: block, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	go func() {
		_, _ = implementer.Implement(context.Background(), ImplementationRequest{RunID: "run-1", Workspace: "/tmp/run-1", Instruction: "fix the bug"}, nil, nil)
	}()
	<-block.started
	if _, err := implementer.Implement(context.Background(), ImplementationRequest{RunID: "run-1", Workspace: "/tmp/run-1", Instruction: "fix the bug"}, nil, nil); err == nil {
		t.Fatal("expected already-active error")
	}
	close(block.unblock)
}

func TestNativeImplementerInterruptCancelsActiveRun(t *testing.T) {
	block := &blockingModel{unblock: make(chan struct{}), started: make(chan struct{})}
	loop := agent.NewSelectedLoop(map[string]agent.ModelTarget{"deepseek-pro": {Model: block, ModelID: "provider-pro"}}, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "deepseek-pro", nil })

	done := make(chan error, 1)
	go func() {
		_, err := implementer.Implement(context.Background(), ImplementationRequest{RunID: "run-1", Workspace: "/tmp/run-1", Instruction: "fix the bug"}, nil, nil)
		done <- err
	}()
	<-block.started
	if err := implementer.Interrupt("run-1"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if err := implementer.Interrupt("run-1"); err == nil {
		t.Fatal("expected not-found error after completion")
	}
}

func TestNativeImplementerFailsWhenAliasResolutionFails(t *testing.T) {
	loop := agent.NewSelectedLoop(nil, NewImplementationTools(&fakeWorkspaceRunner{}, &fakeRepositoryReader{}), nil, 8)
	implementer := NewNativeImplementer(loop, func(context.Context) (string, error) { return "", errors.New("no model selected") })
	if _, err := implementer.Implement(context.Background(), ImplementationRequest{RunID: "run-1", Workspace: "/tmp/run-1", Instruction: "fix the bug"}, nil, nil); err == nil {
		t.Fatal("expected alias resolution error")
	}
}

type sequencedModel struct {
	responses []ports.ModelResponse
}

func (m *sequencedModel) Generate(_ context.Context, _ ports.ModelRequest) (ports.ModelResponse, error) {
	if len(m.responses) == 0 {
		return ports.ModelResponse{}, errors.New("no response queued")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

type blockingModel struct {
	unblock chan struct{}
	started chan struct{}
}

func (m *blockingModel) Generate(ctx context.Context, _ ports.ModelRequest) (ports.ModelResponse, error) {
	close(m.started)
	select {
	case <-ctx.Done():
		return ports.ModelResponse{}, ctx.Err()
	case <-m.unblock:
		return ports.ModelResponse{}, errors.New("unexpected unblock")
	}
}
