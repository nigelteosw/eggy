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

type recoverableError struct{ error }

func Recoverable(err error) error {
	if err == nil {
		return nil
	}
	return recoverableError{error: err}
}

type Config struct {
	FlashModel            string
	ProModel              string
	MaxToolSteps          int
	EscalateAfterSteps    int
	EscalateAfterFailures int
}

type Loop struct {
	flash ports.Model
	pro   ports.Model
	tools map[string]ports.Tool
	defs  []ports.ToolDefinition
	cfg   Config
}

func NewLoop(flash, pro ports.Model, tools []ports.Tool, config Config) *Loop {
	if config.MaxToolSteps <= 0 {
		config.MaxToolSteps = 4
	}
	if config.EscalateAfterSteps <= 0 {
		config.EscalateAfterSteps = config.MaxToolSteps + 1
	}
	if config.EscalateAfterFailures <= 0 {
		config.EscalateAfterFailures = 2
	}
	registry := make(map[string]ports.Tool, len(tools))
	definitions := make([]ports.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		definition := tool.Definition()
		registry[definition.Name] = tool
		definitions = append(definitions, definition)
	}
	return &Loop{flash: flash, pro: pro, tools: registry, defs: definitions, cfg: config}
}

func (l *Loop) Run(ctx context.Context, input string, history []ports.Message, explicitPro bool) (ports.Message, error) {
	messages := append([]ports.Message(nil), history...)
	messages = append(messages, ports.Message{Role: ports.RoleUser, Content: input})
	model, modelID, escalated := l.flash, l.cfg.FlashModel, false
	if explicitPro {
		model, modelID, escalated = l.pro, l.cfg.ProModel, true
	}
	steps, failures := 0, 0
	for {
		response, err := model.Generate(ctx, ports.ModelRequest{Model: modelID, Messages: messages, Tools: l.defs})
		if err != nil {
			return ports.Message{}, err
		}
		assistant := response.Message
		if len(assistant.ToolCalls) == 0 {
			return assistant, nil
		}
		if steps >= l.cfg.MaxToolSteps {
			return ports.Message{}, ErrToolStepLimit
		}
		messages = append(messages, assistant)
		for _, call := range assistant.ToolCalls {
			tool, ok := l.tools[call.Name]
			if !ok {
				return ports.Message{}, fmt.Errorf("%w: %s", ErrUnknownTool, call.Name)
			}
			result, err := tool.Execute(ctx, call.Arguments)
			if err != nil {
				var recoverable recoverableError
				if errors.As(err, &recoverable) {
					failures++
				}
				result, _ = json.Marshal(map[string]string{"error": err.Error()})
			}
			messages = append(messages, ports.Message{Role: ports.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: string(result)})
		}
		steps++
		if !escalated && (steps >= l.cfg.EscalateAfterSteps || failures >= l.cfg.EscalateAfterFailures) {
			model, modelID, escalated = l.pro, l.cfg.ProModel, true
		}
	}
}
