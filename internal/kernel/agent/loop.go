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

// Event is one observable moment in a turn: the model spoke, a tool started,
// a tool returned, or a tool failed. There is one event stream for every
// turn, because there is one loop -- editing a repository is not a different
// kind of turn, it is a turn whose thread has a writable workspace attached.
type Event struct {
	Kind    string
	Call    ports.ToolCall
	Output  string
	Err     error
	Message ports.Message
}

const (
	EventAssistantMessage = "assistant_message"
	EventToolStart        = "tool_start"
	EventToolEnd          = "tool_end"
	EventToolError        = "tool_error"
)

type RunOptions struct {
	AllowedTools map[string]bool
	// OnEvent, if set, fires for every step of the turn. It is how a caller
	// renders live progress and records a transcript; the loop itself keeps
	// no history beyond the messages it sends to the model.
	OnEvent func(Event)
}

type RunResult struct {
	Message ports.Message
	Usage   ports.ModelUsage
	// ReasoningContent is the chain-of-thought behind Message, from whichever
	// model turn produced the final answer, when the provider returns one.
	ReasoningContent string
}

type Loop struct {
	tools    map[string]ports.Tool
	defs     []ports.ToolDefinition
	selected map[string]ModelTarget
	maxSteps int
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
		tools:    registry,
		defs:     definitions,
		selected: targets,
		maxSteps: maxToolSteps,
	}
}

// Run drives one turn to completion. There is exactly one termination
// condition: the model stops calling tools. Nothing designates a tool as
// terminal, because nothing distinguishes "the coding run finished" from
// "the model is done for now" -- shipping a change is an action the model
// takes mid-turn, whose result it reads and reports like any other.
//
// A tool that fails does not end the turn: the error is handed back as that
// tool's result so the model can react to it, which is the whole reason a
// failed patch is recoverable without a new run.
func (l *Loop) Run(ctx context.Context, alias, effort, input string, history []ports.Message, options RunOptions) (RunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return RunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	definitions := l.filteredDefinitions(options)
	messages := append([]ports.Message(nil), history...)
	if input != "" {
		messages = append(messages, ports.Message{Role: ports.RoleUser, Content: input})
	}
	emit := func(event Event) {
		if options.OnEvent != nil {
			options.OnEvent(event)
		}
	}
	result := RunResult{}
	steps := 0
	for {
		response, err := target.Model.Generate(ctx, ports.ModelRequest{Model: target.ModelID, Messages: messages, Tools: definitions, ReasoningEffort: effort})
		if err != nil {
			return result, err
		}
		result.Usage = result.Usage.Add(response.Usage)
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			result.Message = assistant
			result.ReasoningContent = response.ReasoningContent
			return result, nil
		}
		if steps >= l.maxSteps {
			return result, ErrToolStepLimit
		}
		messages = append(messages, assistant)
		emit(Event{Kind: EventAssistantMessage, Message: assistant})
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			if options.AllowedTools != nil && !options.AllowedTools[call.Name] {
				return result, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			emit(Event{Kind: EventToolStart, Call: call})
			output, toolErr := tool.Execute(ctx, call.Arguments)
			kind := EventToolEnd
			if toolErr != nil {
				output, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
				kind = EventToolError
			}
			toolMessage := ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(output)}
			messages = append(messages, toolMessage)
			emit(Event{Kind: kind, Call: call, Output: string(output), Err: toolErr, Message: toolMessage})
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
