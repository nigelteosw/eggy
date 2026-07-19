package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nigelteosw/eggy/internal/kernel/lane"
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
	Lane         lane.Lane
}

type RunResult struct {
	Message ports.Message
	Usage   ports.ModelUsage
}

type Loop struct {
	tools               map[string]ports.Tool
	defs                []ports.ToolDefinition
	selected            map[string]ModelTarget
	selectedMaxSteps    int
	implementationTools map[string]bool
}

func NewSelectedLoop(models map[string]ModelTarget, tools []ports.Tool, implementationToolNames []string, maxToolSteps int) *Loop {
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
	implTools := make(map[string]bool, len(implementationToolNames))
	for _, name := range implementationToolNames {
		implTools[name] = true
	}
	return &Loop{
		tools:               registry,
		defs:                definitions,
		selected:            targets,
		selectedMaxSteps:    maxToolSteps,
		implementationTools: implTools,
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
			if options.Lane != lane.Implementation && l.implementationTools[call.Name] {
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

func (l *Loop) filteredDefinitions(options RunOptions) []ports.ToolDefinition {
	defs := append([]ports.ToolDefinition(nil), l.defs...)

	// Filter out implementation tools when not in implementation lane.
	if options.Lane != lane.Implementation {
		filtered := make([]ports.ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if !l.implementationTools[d.Name] {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}

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
