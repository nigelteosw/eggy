package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/ports"
)

var (
	ErrUnknownTool   = errors.New("model requested an unknown tool")
	ErrToolStepLimit = errors.New("assistant tool-step limit reached")
)

type ModelTarget struct {
	Model   ports.Model
	ModelID string
}

type RunOptions struct {
	AllowedTools map[string]bool
}

type RunResult struct {
	Message ports.Message
	Usage   ports.ModelUsage
}

type Loop struct {
	tools            map[string]ports.Tool
	defs             []ports.ToolDefinition
	selected         map[string]ModelTarget
	selectedMaxSteps int
}

func NewSelectedLoop(models map[string]ModelTarget, tools []ports.Tool, maxToolSteps int) *Loop {
	if maxToolSteps <= 0 {
		maxToolSteps = 4
	}
	registry := make(map[string]ports.Tool, len(tools))
	definitions := make([]ports.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		definition := tool.Definition()
		registry[definition.Name] = tool
		definitions = append(definitions, definition)
	}
	targets := make(map[string]ModelTarget, len(models))
	for alias, target := range models {
		targets[alias] = target
	}
	return &Loop{
		tools:            registry,
		defs:             definitions,
		selected:         targets,
		selectedMaxSteps: maxToolSteps,
	}
}

func (l *Loop) RunSelected(ctx context.Context, alias, input string, history []ports.Message, options RunOptions) (RunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return RunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	definitions := l.filteredDefinitions(options)
	messages := append([]ports.Message(nil), history...)
	messages = append(messages, ports.Message{Role: ports.RoleUser, Content: input})
	result := RunResult{}
	steps := 0
	for {
		response, err := target.Model.Generate(ctx, ports.ModelRequest{Model: target.ModelID, Messages: messages, Tools: definitions})
		if err != nil {
			return result, err
		}
		result.Usage = result.Usage.Add(response.Usage)
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			result.Message = assistant
			return result, nil
		}
		if steps >= l.selectedMaxSteps {
			return result, ErrToolStepLimit
		}
		messages = append(messages, assistant)
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			if options.AllowedTools != nil && !options.AllowedTools[call.Name] {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			output, toolErr := tool.Execute(ctx, call.Arguments)
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
			}
			messages = append(messages, ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)})
		}
		steps++
	}
}

// ErrTerminalToolNotCalled is returned when RunImplementation exhausts a
// model turn without any tool call, before the terminal tool was ever
// successfully called.
var ErrTerminalToolNotCalled = errors.New("implementation run ended without a terminal tool call")

type ImplementationEvent struct {
	Kind    string
	Call    ports.ToolCall
	Output  string
	Err     error
	Message ports.Message
}

type ImplementationRunResult struct {
	Terminal json.RawMessage
	Usage    ports.ModelUsage
	Messages []ports.Message
}

// RunImplementation drives the loop until the model successfully calls
// terminalTool, returning that call's raw arguments, or the step limit is
// reached first. Every tool registered on l is available unconditionally —
// callers construct a Loop instance scoped to exactly the tools an
// implementation run should have, rather than relying on lane filtering.
// onToolCall, if non-nil, fires after each successful non-terminal tool call
// for progress reporting; it does not fire for the terminal tool itself.
func (l *Loop) RunImplementation(ctx context.Context, alias string, messages []ports.Message, terminalTool string, onToolCall func(name string)) (json.RawMessage, ports.ModelUsage, error) {
	result, err := l.RunImplementationWithEvents(ctx, alias, messages, terminalTool, func(event ImplementationEvent) {
		if onToolCall != nil && event.Kind == "tool_end" {
			onToolCall(event.Call.Name)
		}
	})
	return result.Terminal, result.Usage, err
}

// RunImplementationWithEvents drives the implementation loop while retaining the
// model-visible transcript and reporting structured tool lifecycle events.
func (l *Loop) RunImplementationWithEvents(ctx context.Context, alias string, messages []ports.Message, terminalTool string, onEvent func(ImplementationEvent)) (ImplementationRunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return ImplementationRunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	messages = append([]ports.Message(nil), messages...)
	usage := ports.ModelUsage{}
	steps := 0
	for {
		response, err := target.Model.Generate(ctx, ports.ModelRequest{Model: target.ModelID, Messages: messages, Tools: l.defs})
		if err != nil {
			return ImplementationRunResult{Usage: usage, Messages: messages}, err
		}
		usage = usage.Add(response.Usage)
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			return ImplementationRunResult{Usage: usage, Messages: messages}, ErrTerminalToolNotCalled
		}
		if steps >= l.selectedMaxSteps {
			return ImplementationRunResult{Usage: usage, Messages: messages}, ErrToolStepLimit
		}
		messages = append(messages, assistant)
		if onEvent != nil {
			onEvent(ImplementationEvent{Kind: "assistant_message", Message: assistant})
		}
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return ImplementationRunResult{Usage: usage, Messages: messages}, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			if onEvent != nil {
				onEvent(ImplementationEvent{Kind: "tool_start", Call: call})
			}
			output, toolErr := tool.Execute(ctx, call.Arguments)
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
				toolMessage := ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)}
				messages = append(messages, toolMessage)
				if onEvent != nil {
					onEvent(ImplementationEvent{Kind: "tool_error", Call: call, Output: string(output), Err: toolErr, Message: toolMessage})
				}
				continue
			}
			if call.Name == terminalTool {
				if onEvent != nil {
					onEvent(ImplementationEvent{Kind: "terminal", Call: call, Output: string(output), Message: assistant})
				}
				return ImplementationRunResult{Terminal: call.Arguments, Usage: usage, Messages: messages}, nil
			}
			toolMessage := ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)}
			messages = append(messages, toolMessage)
			if onEvent != nil {
				onEvent(ImplementationEvent{Kind: "tool_end", Call: call, Output: string(output), Message: toolMessage})
			}
		}
		steps++
	}
}

// ToolNames returns the tools available for a turn after applying the same
// allowlist filter used for the model request.
func (l *Loop) ToolNames(options RunOptions) []string {
	definitions := l.filteredDefinitions(options)
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func (l *Loop) filteredDefinitions(options RunOptions) []ports.ToolDefinition {
	defs := append([]ports.ToolDefinition(nil), l.defs...)

	// Apply explicit tool allowlist.
	if options.AllowedTools != nil {
		filtered := make([]ports.ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if options.AllowedTools[d.Name] {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}

	return defs
}
