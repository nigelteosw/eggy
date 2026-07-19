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
	return &Loop{tools: registry, defs: definitions, selected: targets, selectedMaxSteps: maxToolSteps}
}

func (l *Loop) RunSelected(ctx context.Context, alias, input string, history []ports.Message, options RunOptions) (RunResult, error) {
	target, ok := l.selected[alias]
	if !ok || target.Model == nil || target.ModelID == "" {
		return RunResult{}, fmt.Errorf("model alias %q is not configured", alias)
	}
	definitions := l.filteredDefinitions(options.AllowedTools)
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
			if !ok || (options.AllowedTools != nil && !options.AllowedTools[call.Name]) {
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

func (l *Loop) filteredDefinitions(allowed map[string]bool) []ports.ToolDefinition {
	if allowed == nil {
		return append([]ports.ToolDefinition(nil), l.defs...)
	}
	definitions := make([]ports.ToolDefinition, 0, len(l.defs))
	for _, definition := range l.defs {
		if allowed[definition.Name] {
			definitions = append(definitions, definition)
		}
	}
	return definitions
}
