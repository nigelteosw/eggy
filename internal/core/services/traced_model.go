package services

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// TracedModel records every call made through one model adapter. It is a
// ports.Model wrapping a ports.Model, applied in bootstrap, so no adapter
// knows tracing exists and no adapter can forget to support it.
type TracedModel struct {
	model    ports.Model
	recorder *TraceRecorder
}

// NewTracedModel wraps model. A nil recorder returns model unchanged, so an
// unconfigured deployment carries no wrapper at all.
func NewTracedModel(model ports.Model, recorder *TraceRecorder) ports.Model {
	if recorder == nil || model == nil {
		return model
	}
	return TracedModel{model: model, recorder: recorder}
}

func (m TracedModel) Generate(ctx context.Context, request ports.ModelRequest) (ports.ModelResponse, error) {
	turn := m.recorder.turn(ctx)
	if turn == nil {
		return m.model.Generate(ctx, request)
	}
	started := m.recorder.now().UTC()
	response, err := m.model.Generate(ctx, request)
	turn.SetModel(request.Model)
	turn.RecordModelCall(request, response, err, started, m.recorder.now().UTC().Sub(started))
	return response, err
}

type tracedRequest struct {
	Model           string             `json:"model"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
	Messages        []tracedMessage    `json:"messages"`
	ToolNames       []string           `json:"tool_names"`
	Tools           []tracedToolSchema `json:"tools,omitempty"`
}

type tracedToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema,omitempty"`
}

type tracedResponse struct {
	Message          tracedMessage `json:"message"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
}

type tracedMessage struct {
	Role       ports.Role       `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []ports.ToolCall `json:"tool_calls,omitempty"`
}

func modelRequestRecord(request ports.ModelRequest, includeSchemas bool) tracedRequest {
	record := tracedRequest{
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
		Messages:        make([]tracedMessage, 0, len(request.Messages)),
		ToolNames:       make([]string, 0, len(request.Tools)),
	}
	for _, message := range request.Messages {
		record.Messages = append(record.Messages, traceMessage(message))
	}
	for _, tool := range request.Tools {
		record.ToolNames = append(record.ToolNames, tool.Name)
	}
	if includeSchemas {
		record.Tools = make([]tracedToolSchema, 0, len(request.Tools))
		for _, tool := range request.Tools {
			record.Tools = append(record.Tools, tracedToolSchema{Name: tool.Name, Description: tool.Description, Schema: tool.Schema})
		}
	}
	return record
}

func modelResponseRecord(response ports.ModelResponse) tracedResponse {
	return tracedResponse{Message: traceMessage(response.Message), ReasoningContent: response.ReasoningContent}
}

func traceMessage(message ports.Message) tracedMessage {
	content := message.Content
	if len(message.Parts) > 0 {
		content = strings.TrimSpace(content)
		if content == "" || content == "Describe this image." {
			content = "[image attached]"
		} else {
			content += "\n[image attached]"
		}
	}
	return tracedMessage{
		Role: message.Role, Content: content, Name: message.Name,
		ToolCallID: message.ToolCallID, ToolCalls: message.ToolCalls,
	}
}
