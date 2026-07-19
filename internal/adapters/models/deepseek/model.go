package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type Model struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

func New(endpoint, apiKey string, client *http.Client) *Model {
	if client == nil {
		client = http.DefaultClient
	}
	return &Model{endpoint: endpoint, apiKey: apiKey, http: client}
}

type requestBody struct {
	Model    string            `json:"model"`
	Messages []providerMessage `json:"messages"`
	Tools    []providerTool    `json:"tools,omitempty"`
}

type providerMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content,omitempty"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []providerToolCall `json:"tool_calls,omitempty"`
}

type providerTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type providerToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (m *Model) Generate(ctx context.Context, input ports.ModelRequest) (ports.ModelResponse, error) {
	body := requestBody{Model: input.Model}
	for _, message := range input.Messages {
		translated := providerMessage{Role: string(message.Role), Content: message.Content, Name: message.Name, ToolCallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			providerCall := providerToolCall{ID: call.ID, Type: "function"}
			providerCall.Function.Name = call.Name
			providerCall.Function.Arguments = string(call.Arguments)
			translated.ToolCalls = append(translated.ToolCalls, providerCall)
		}
		body.Messages = append(body.Messages, translated)
	}
	for _, tool := range input.Tools {
		translated := providerTool{Type: "function"}
		translated.Function.Name = tool.Name
		translated.Function.Description = tool.Description
		translated.Function.Parameters = tool.Schema
		body.Tools = append(body.Tools, translated)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("encode model request: %w", err)
	}
	var response *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(encoded))
		if err != nil {
			return ports.ModelResponse{}, err
		}
		request.Header.Set("Authorization", "Bearer "+m.apiKey)
		request.Header.Set("Content-Type", "application/json")
		response, err = m.http.Do(request)
		transient := err != nil || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		if !transient || attempt == 2 {
			if err != nil {
				return ports.ModelResponse{}, fmt.Errorf("DeepSeek request: %w", err)
			}
			break
		}
		if response != nil {
			response.Body.Close()
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ports.ModelResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ports.ModelResponse{}, fmt.Errorf("DeepSeek returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message providerMessage `json:"message"`
		} `json:"choices"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&result); err != nil {
		return ports.ModelResponse{}, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if len(result.Choices) == 0 {
		return ports.ModelResponse{}, errors.New("DeepSeek returned no choices")
	}
	providerResult := result.Choices[0].Message
	message := ports.Message{Role: ports.Role(providerResult.Role), Content: providerResult.Content, Name: providerResult.Name, ToolCallID: providerResult.ToolCallID}
	for _, call := range providerResult.ToolCalls {
		arguments := json.RawMessage(call.Function.Arguments)
		if !json.Valid(arguments) {
			return ports.ModelResponse{}, fmt.Errorf("DeepSeek returned invalid arguments for tool %q", call.Function.Name)
		}
		message.ToolCalls = append(message.ToolCalls, ports.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: arguments})
	}
	return ports.ModelResponse{Message: message}, nil
}

func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[REDACTED]")
}
